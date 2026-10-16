// Package web 是 user-center 的 SSR 页面 + JSON API(work-10 / wc-2..5)。
// 不做前后分离: 服务端渲染 4 页, vanilla JS 5s 轮询 JSON 端点刷新。
// 鉴权: token 登录 → 服务端经 Marzban 校验后种 HttpOnly cookie 会话; token 只在 cookie, 页面不渲染明文。
//
// Web 层 = gin + webkit 共享套件(ADR-0001): 会话/限流/安全头来自 webkit,
// 本包只保留业务(userFor 缓存、Marzban 交互、页面数据装配)。
package web

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	qr "github.com/skip2/go-qrcode"
	"vpnmaster/user-center/internal/billing"
	"vpnmaster/user-center/internal/marzban"
	"vpnmaster/webkit"
	"vpnmaster/webui"
)

const sessionCookie = "uc_sess"

// csp 含 'unsafe-inline' style/script(SSR 内联样式与轮询脚本), img-src 允许 self+data(QR 走 /qr.png self)。
const csp = "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self' data:; object-src 'none'; frame-ancestors 'self'"

// Server 是 user-center 的 handler 集合。
type Server struct {
	Marzban     marzban.Client
	Plans       []marzban.Plan
	PlansByName map[string]marzban.Plan
	Template    *template.Template

	session  *webkit.Session
	rl       *webkit.RateLimiter
	accounts AccountBackend   // nil = 账号体系未启用(WI-2 渐进集成)
	billing  *billing.Service // nil = 订阅未启用(旧 bind/purchase 路径)

	mu    sync.Mutex
	cache map[string]cacheEntry // token -> 最近一次解析(30s), 防 5s 轮询打爆 Marzban
}

type cacheEntry struct {
	at   time.Time
	user marzban.User
	err  error
}

// New 构造; 依赖注入 marzban client(可 Mock) 与 plans。secure 线上 HTTPS 置 true。
func New(mz marzban.Client, plans []marzban.Plan, secure bool) (*Server, error) {
	pb := map[string]marzban.Plan{}
	for _, p := range plans {
		pb[p.Name] = p
	}
	tpl, err := webui.ComponentsTemplate(userLayout)
	if err != nil {
		return nil, err
	}
	return &Server{
		Marzban: mz, Plans: plans, PlansByName: pb,
		Template: tpl,
		session:  webkit.NewSession(sessionCookie, 72*time.Hour, secure),
		rl:       webkit.NewRateLimiter(10, time.Minute), // wc-5.1: 每 IP 10 次/分
		cache:    map[string]cacheEntry{},
	}, nil
}

// Router 挂载全部路由(gin.Engine 即 http.Handler)。
func (s *Server) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	e := gin.New()
	e.Use(gin.Recovery(), webkit.SecurityHeaders(csp))
	s.mountAccounts(e) // authkit 路由(未接账号体系时 no-op)
	s.mountBilling(e)  // /billing + /webhooks/waffo(未启用订阅时 no-op)

	e.GET("/", s.root)
	e.GET("/login", s.loginPage)
	e.POST("/login", s.loginPost)
	e.POST("/logout", s.logout)

	// vendored 静态资产(ADR-0002: 零构建, 无 CDN)
	e.GET("/assets/bootstrap.min.css", gin.WrapH(webui.AssetHandler("bootstrap.min.css")))
	e.GET("/assets/bootstrap.bundle.min.js", gin.WrapH(webui.AssetHandler("bootstrap.bundle.min.js")))
	e.GET("/assets/wm-ui.js", gin.WrapH(webui.AssetHandler("wm-ui.js")))

	auth := e.Group("", s.auth())
	auth.GET("/dashboard", s.dashboard)
	if s.accounts != nil {
		// 绑定页单独门槛: 需 acct 会话但不要求已绑定(避开 auth() 重定向环)。
		bind := e.Group("", s.bindOnly())
		bind.GET("/bind", s.bindPage)
		bind.POST("/bind", s.bindPost)
		// 运营用户管理(WI-5): admin 角色门槛。
		adm := e.Group("/admin", s.adminOnly())
		adm.GET("/users", s.adminUsersPage)
		adm.POST("/users/create", s.adminUserCreate)
		adm.POST("/users/ban", s.adminUserBan)
		adm.POST("/users/unban", s.adminUserUnban)
		adm.POST("/users/delete", s.adminUserDelete)
		adm.POST("/users/resetpw", s.adminUserResetPw)
		adm.POST("/users/grant", s.adminUserGrant)
	}
	auth.GET("/subscription", s.subscription)
	auth.POST("/subscription/token", s.retoken)
	auth.GET("/purchase", s.purchase)
	auth.POST("/purchase", s.createInvoice)
	auth.GET("/api/status", s.apiStatus)
	auth.GET("/api/invoices", s.apiInvoices)
	auth.GET("/qr.png", s.qrPNG)

	return e
}

// ---- 会话/用户 ----

// currentToken 取会话对应的 Marzban token(账号模式经映射表解析)。
func (s *Server) currentToken(r *http.Request) (string, bool) {
	return s.resolveToken(r)
}

// userFor 会话 → 用户(30s 缓存)。账号模式先经映射表解出 Marzban token。
func (s *Server) userFor(ctx context.Context, r *http.Request) (marzban.User, bool, error) {
	tok, ok := s.resolveToken(r)
	if !ok {
		return marzban.User{}, false, nil
	}
	s.mu.Lock()
	if e, hit := s.cache[tok]; hit && time.Since(e.at) < 30*time.Second {
		s.mu.Unlock()
		return e.user, true, e.err
	}
	s.mu.Unlock()
	u, err := s.Marzban.GetUserByToken(ctx, tok)
	s.mu.Lock()
	s.cache[tok] = cacheEntry{at: time.Now(), user: u, err: err}
	s.mu.Unlock()
	return u, true, err
}

// auth 会话门槛: 无会话 303 → /login; token 在 Marzban 失效也踢回登录(语义同原手撸 auth())。
func (s *Server) auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if id, unbound := s.accountUnbound(c); unbound {
			_ = id // 登录已成但无订阅供给: 订阅启用→去购买页; 否则回旧 bind 迁移通道
			dest := "/bind"
			if s.billing != nil {
				dest = "/billing"
			}
			c.Redirect(http.StatusSeeOther, dest)
			c.Abort()
			return
		}
		u, _, err := s.userFor(c.Request.Context(), c.Request)
		if err != nil || u.Username == "" {
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}
		c.Set(ctxUserKey, u)
		c.Next()
	}
}

const ctxUserKey = "uc.user"

func userFrom(c *gin.Context) marzban.User {
	if v, ok := c.Get(ctxUserKey); ok {
		if u, ok := v.(marzban.User); ok {
			return u
		}
	}
	return marzban.User{}
}

// ---- 路由处理 ----

func (s *Server) root(c *gin.Context) {
	if _, ok := s.currentToken(c.Request); ok {
		c.Redirect(http.StatusSeeOther, "/dashboard")
		return
	}
	c.Redirect(http.StatusSeeOther, "/login")
}

func (s *Server) loginPage(c *gin.Context) {
	s.page(c, Page{Title: "登录", Content: "login",
		PageData: map[string]any{"Err": c.Query("err") != "", "Accounts": s.accounts != nil}})
}

func (s *Server) loginPost(c *gin.Context) {
	jsonMode := webkit.WantsJSON(c) // S3: fetch 提交失败时页面保留输入, 只出 toast
	replyFail := func(msg string) {
		if jsonMode {
			c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": msg})
			return
		}
		c.Redirect(http.StatusSeeOther, "/login?err="+url.QueryEscape(msg))
	}
	if !s.rl.Allow(webkit.ClientIP(c.Request)) {
		replyFail("限流,请稍后再试")
		return
	}
	if s.loginAcct(c, jsonMode, replyFail) {
		return // WI-3: 账号模式分支已处理(邮箱密码)
	}
	token := c.PostForm("token")
	u, err := s.Marzban.GetUserByToken(c.Request.Context(), token)
	if err != nil || u.Username == "" {
		replyFail("token无效")
		return
	}
	s.session.Revoke(c.Writer, c.Request) // 旧 sid 作废(重复登录不留双会话)
	s.session.Issue(c.Writer, token)
	if jsonMode {
		c.JSON(http.StatusOK, gin.H{"ok": true, "redirect": "/dashboard"})
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard")
}

func (s *Server) logout(c *gin.Context) {
	s.session.Revoke(c.Writer, c.Request)
	c.Redirect(http.StatusSeeOther, "/login")
}

// ---- SSR 页 ----

func (s *Server) page(c *gin.Context, p Page) {
	p.CSS = webui.ThemeCSS()
	p.HasBilling = s.billing != nil
	if s.accounts != nil && !p.IsAdmin {
		if v, ok := s.sessionValue(c.Request); ok {
			if id, isAcct := acctID(v); isAcct {
				p.IsAdmin = s.accounts.IsAdmin(c.Request.Context(), id)
			}
		}
	}
	webui.WritePage(c.Writer, s.Template, "layout", p)
}

func (s *Server) dashboard(c *gin.Context) {
	u := userFrom(c)
	var d DashboardData
	d.Username = u.Username
	d.Status = u.Status
	d.Online = u.Online
	d.UsedStr = humanBytes(u.UsedTraffic)
	d.LimitStr = humanBytes(u.LimitTraffic)
	if u.LimitTraffic > 0 {
		d.QuotaPct = int(float64(u.UsedTraffic) / float64(u.LimitTraffic) * 100)
		if d.QuotaPct > 100 {
			d.QuotaPct = 100
		}
	}
	d.ExpireStr = fmtExpire(u.ExpireDays)
	s.page(c, Page{Title: "概览", Active: "dashboard", Content: "pageDashboard", PageData: d})
}

func (s *Server) subscription(c *gin.Context) {
	token, _ := s.currentToken(c.Request)
	var d SubData
	d.SubURL = s.Marzban.SubURL(token)
	d.Changed = c.Query("changed") != ""
	d.Err = c.Query("err")
	d.HasBilling = s.billing != nil
	d.Guides = []Guide{
		{Title: "Clash Verge", Desc: "导入订阅链接(右键 → 复制 → 添加订阅)"},
		{Title: "Stash", Desc: "配置 → 添加远程配置 → 粘贴订阅链接"},
		{Title: "Windows 客户端(Phase 2)", Desc: "即将上线, 一键导入"},
	}
	s.page(c, Page{Title: "订阅", Active: "subscription", Content: "pageSubscription", PageData: d})
}

// retoken wc-3.3: 更换 token。legacy=重校验+重种会话; 账号模式=写映射表不动会话。
func (s *Server) retoken(c *gin.Context) {
	token := strings.TrimSpace(c.PostForm("token"))
	jsonMode := webkit.WantsJSON(c)
	u, err := s.Marzban.GetUserByToken(c.Request.Context(), token)
	if err != nil || u.ID == 0 {
		if jsonMode {
			c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "token无效"})
			return
		}
		c.Redirect(http.StatusSeeOther, "/subscription?err=token无效")
		return
	}
	if s.accounts != nil {
		v, _ := s.sessionValue(c.Request)
		if id, isAcct := acctID(v); isAcct {
			if err := s.accounts.SetLink(c.Request.Context(), id, token); err != nil {
				if jsonMode {
					c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "保存失败"})
					return
				}
				c.Redirect(http.StatusSeeOther, "/subscription?err=保存失败")
				return
			}
			if jsonMode {
				c.JSON(http.StatusOK, gin.H{"ok": true, "flash": "已更换 token, 立即生效"})
				return
			}
			c.Redirect(http.StatusSeeOther, "/subscription?changed=1")
			return
		}
	}
	s.session.Revoke(c.Writer, c.Request)
	s.session.Issue(c.Writer, token)
	if jsonMode {
		c.JSON(http.StatusOK, gin.H{"ok": true, "flash": "已更换 token, 立即生效"})
		return
	}
	c.Redirect(http.StatusSeeOther, "/subscription?changed=1")
}

func (s *Server) purchase(c *gin.Context) {
	if s.billing != nil { // 订阅模式: 旧一次性购买入口退役, 统一去 /billing
		c.Redirect(http.StatusSeeOther, "/billing")
		return
	}
	token, _ := s.currentToken(c.Request)
	invs, _ := s.Marzban.ListInvoices(c.Request.Context(), token)
	views := make([]PlanView, 0, len(s.Plans))
	for _, p := range s.Plans {
		gb := ""
		if p.Bytes > 0 {
			gb = fmt.Sprintf("%d", p.Bytes>>30)
		}
		views = append(views, PlanView{Name: p.Name, Price: p.Price, GBStr: gb, Days: p.Days})
	}
	var d PurchaseData
	d.Plans = views
	d.Invoices = invs
	d.JustOrdered = c.Query("new") != ""
	d.Err = c.Query("err")
	s.page(c, Page{Title: "购买", Active: "purchase", Content: "pagePurchase", PageData: d})
}

func (s *Server) createInvoice(c *gin.Context) {
	plan := c.PostForm("plan")
	jsonMode := webkit.WantsJSON(c)
	fail := func(msg string) {
		if jsonMode {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": msg})
			return
		}
		c.Redirect(http.StatusSeeOther, "/purchase?err="+url.QueryEscape(msg))
	}
	if _, ok := s.PlansByName[plan]; !ok {
		fail("无效套餐")
		return
	}
	token, _ := s.currentToken(c.Request)
	if _, err := s.Marzban.CreateInvoice(c.Request.Context(), token, plan); err != nil {
		fail("下单失败,请稍后重试")
		return
	}
	if jsonMode {
		c.JSON(http.StatusOK, gin.H{"ok": true, "flash": "已下单, 等待到账…(5s 轮询)"})
		return
	}
	c.Redirect(http.StatusSeeOther, "/purchase?new=1")
}

// ---- JSON API(轮询) ----

func (s *Server) apiStatus(c *gin.Context) {
	u := userFrom(c)
	// 轮询直接取最近一次 userFor 结果; 超过 30s 重新解析
	fresh, _, _ := s.userFor(c.Request.Context(), c.Request)
	if fresh.Username != "" {
		u = fresh
	}
	c.JSON(http.StatusOK, map[string]any{
		"username": u.Username, "status": u.Status, "online": u.Online,
		"expire": fmtExpire(u.ExpireDays),
		"used":   humanBytes(u.UsedTraffic), "limit": humanBytes(u.LimitTraffic),
	})
}

func (s *Server) apiInvoices(c *gin.Context) {
	token, _ := s.currentToken(c.Request)
	invs, err := s.Marzban.ListInvoices(c.Request.Context(), token)
	if err != nil {
		c.JSON(http.StatusOK, []InvoiceView{})
		return
	}
	views := make([]InvoiceView, 0, len(invs))
	for _, i := range invs {
		views = append(views, InvoiceView{
			ID: i.ID, PlanName: i.PlanName, Amount: i.Amount, Status: i.Status,
			CreatedAt: time.Unix(i.CreatedAt, 0).Format("2006-01-02 15:04"),
		})
	}
	c.JSON(http.StatusOK, views)
}

// qrPNG wc-3.1: 服务端出二维码(go-qrcode), img-src 'self' 友好。
func (s *Server) qrPNG(c *gin.Context) {
	subURL := s.Marzban.SubURL(func() string {
		t, _ := s.currentToken(c.Request)
		return t
	}())
	png, err := qrcodePNG(subURL, 256)
	if err != nil {
		c.String(http.StatusInternalServerError, "qr")
		return
	}
	c.Data(http.StatusOK, "image/png", png)
}

func qrcodePNG(content string, size int) ([]byte, error) {
	return qr.Encode(content, qr.Medium, size)
}
