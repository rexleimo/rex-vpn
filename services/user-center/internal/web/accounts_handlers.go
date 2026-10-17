// accounts_handlers.go: 邮箱密码登录 + 账号↔订阅绑定(WI-3/WI-4)。
// 会话约定: 账号模式 session value = "acct:<authkit-userID>"; Marzban token 存服务端映射表, 浏览器不再携带。
package web

import (
	"context"
	"github.com/gin-gonic/gin"
	authkitgin "github.com/open-rails/authkit/adapters/gin"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"vpnmaster/user-center/internal/accounts"
	"vpnmaster/webkit"
)

const acctPrefix = "acct:"

func acctID(value string) (string, bool) {
	if !strings.HasPrefix(value, acctPrefix) {
		return "", false
	}
	id := strings.TrimPrefix(value, acctPrefix)
	return id, id != ""
}

// sessionValue 取当前 session value(两种模式通用)。
func (s *Server) sessionValue(r *http.Request) (string, bool) {
	v, _, ok := s.session.Value(r)
	return v, ok
}

// resolveToken 把会话解析为 Marzban token: legacy=值本身; 账号=映射表查询。
func (s *Server) resolveToken(r *http.Request) (string, bool) {
	v, ok := s.sessionValue(r)
	if !ok {
		return "", false
	}
	if id, isAcct := acctID(v); isAcct && s.accounts != nil {
		tok, found, err := s.accounts.LinkToken(r.Context(), id)
		if err != nil || !found {
			return "", false
		}
		return tok, true
	}
	return v, true
}

// accountIDIfBound 供 auth() 门槛: 账号会话未绑定订阅时返回其 accountID → 303 /bind。
func (s *Server) accountUnbound(c *gin.Context) (string, bool) {
	if s.accounts == nil {
		return "", false
	}
	v, ok := s.sessionValue(c.Request)
	if !ok {
		return "", false
	}
	id, isAcct := acctID(v)
	if !isAcct {
		return "", false
	}
	_, found, err := s.accounts.LinkToken(c.Request.Context(), id)
	if err == nil && found {
		return "", false
	}
	return id, true
}

// loginAcct 账号模式登录分支: authkit 校验 → 种 acct 会话。返回是否已处理。
func (s *Server) loginAcct(c *gin.Context, jsonMode bool, replyFail func(string)) bool {
	if s.accounts == nil {
		return false
	}
	identifier := strings.TrimSpace(c.PostForm("identifier"))
	password := c.PostForm("password")
	if identifier == "" || password == "" {
		replyFail("请输入邮箱和密码")
		return true
	}
	a, err := s.accounts.Login(c.Request.Context(), identifier, password, webkit.ClientIP(c.Request))
	if err != nil {
		// 对外不泄露账号存在性; 细节进服务端日志
		s.logf("login failed %q from %s: %v", identifier, webkit.ClientIP(c.Request), err)
		replyFail("账号或密码错误")
		return true
	}
	s.session.Revoke(c.Writer, c.Request)
	s.session.Issue(c.Writer, acctPrefix+a.UserID)
	if jsonMode {
		c.JSON(http.StatusOK, gin.H{"ok": true, "redirect": "/dashboard"})
		return true
	}
	c.Redirect(http.StatusSeeOther, "/dashboard")
	return true
}

// registerPage 自助注册页: 仅在账号体系启用且 UC_REGISTRATION=open 时可达。
func (s *Server) registerPage(c *gin.Context) {
	if s.accounts == nil || !s.accounts.SelfRegistrationOpen() {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	s.page(c, Page{Title: "注册", Content: "register",
		PageData: map[string]any{"Err": c.Query("err")}})
}

// registerPost 创建账号并直接登录(authkit 注册即登录)。后续门槛由 auth()
// 接管: 未绑定订阅的账号会被导向 /billing(启用订阅)或 /bind(旧迁移)。
func (s *Server) registerPost(c *gin.Context) {
	jsonMode := webkit.WantsJSON(c)
	replyFail := func(msg string) {
		if jsonMode {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": msg})
			return
		}
		c.Redirect(http.StatusSeeOther, "/register?err="+url.QueryEscape(msg))
	}
	if s.accounts == nil || !s.accounts.SelfRegistrationOpen() {
		replyFail("自助注册未开放")
		return
	}
	if !s.rl.Allow(webkit.ClientIP(c.Request)) {
		replyFail("操作过于频繁, 请稍后再试")
		return
	}
	email := strings.ToLower(strings.TrimSpace(c.PostForm("email")))
	password := c.PostForm("password")
	if _, err := mail.ParseAddress(email); err != nil || !strings.Contains(email, "@") {
		replyFail("请输入有效邮箱")
		return
	}
	if len(password) < 8 {
		replyFail("密码至少 8 位")
		return
	}
	a, err := s.accounts.Register(c.Request.Context(), email, password, webkit.ClientIP(c.Request))
	if err != nil {
		s.logf("register failed %q from %s: %v", email, webkit.ClientIP(c.Request), err)
		replyFail("注册失败: 邮箱可能已注册或密码不符合要求")
		return
	}
	s.session.Revoke(c.Writer, c.Request)
	s.session.Issue(c.Writer, acctPrefix+a.UserID)
	if jsonMode {
		c.JSON(http.StatusOK, gin.H{"ok": true, "redirect": "/dashboard"})
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard")
}

// bindOnly 绑定页门槛: 必须是已登录的 acct 会话(未绑定也可进); 其他一律回 /login 或 dashboard。
func (s *Server) bindOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		v, ok := s.sessionValue(c.Request)
		if !ok {
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}
		if _, isAcct := acctID(v); !isAcct {
			c.Redirect(http.StatusSeeOther, "/dashboard") // legacy 会话不需要绑定页
			c.Abort()
			return
		}
		c.Next()
	}
}

// bindPage 首次绑定: 已登录账号但无订阅映射时进入。
func (s *Server) bindPage(c *gin.Context) {
	v, _ := s.sessionValue(c.Request)
	id, _ := acctID(v)
	email, _ := s.accounts.AccountEmail(c.Request.Context(), id)
	s.page(c, Page{Title: "绑定订阅", Active: "dashboard", Content: "bind",
		PageData: map[string]any{"Email": email, "Err": c.Query("err") != ""}})
}

// bindPost 保存绑定: 先经 Marzban 校验 token 有效再落映射表。
func (s *Server) bindPost(c *gin.Context) {
	jsonMode := webkit.WantsJSON(c)
	v, _ := s.sessionValue(c.Request)
	id, _ := acctID(v)
	token := strings.TrimSpace(c.PostForm("token"))
	fail := func(msg string) {
		if jsonMode {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": msg})
			return
		}
		c.Redirect(http.StatusSeeOther, "/bind?err="+url.QueryEscape(msg))
	}
	if token == "" {
		fail("token 不能为空")
		return
	}
	if u, err := s.Marzban.GetUserByToken(c.Request.Context(), token); err != nil || u.Username == "" {
		fail("token 无效或已过期")
		return
	}
	if err := s.accounts.SetLink(c.Request.Context(), id, token); err != nil {
		fail("保存失败")
		return
	}
	if jsonMode {
		c.JSON(http.StatusOK, gin.H{"ok": true, "redirect": "/dashboard"})
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard")
}

func (s *Server) logf(format string, args ...any) { log.Printf(format, args...) }

// ---- accounts_mount(并入): authkit 路由挂载与后端接口 ----
// accounts_mount.go: authkit 账号体系的 web 层接线(WI-2/WI-3)。未接账号体系时全部为 no-op。

// AccountBackend 是 web 层用到的账号体系能力(可 mock, 契约测试不依赖 PG)。
type AccountBackend interface {
	Login(ctx context.Context, identifier, password, clientIP string) (accounts.Auth, error)
	Register(ctx context.Context, email, password, clientIP string) (accounts.Auth, error)
	SelfRegistrationOpen() bool
	LinkToken(ctx context.Context, accountID string) (string, bool, error)
	SetLink(ctx context.Context, accountID, token string) error
	AccountEmail(ctx context.Context, accountID string) (string, error)
	IsAdmin(ctx context.Context, userID string) bool
	ListAccounts(ctx context.Context, search string) ([]accounts.AdminUser, error)
	CreatePasswordUser(ctx context.Context, email, username, password string) (string, error)
	BanAccount(ctx context.Context, userID, reason string) error
	UnbanAccount(ctx context.Context, userID string) error
	DeleteAccount(ctx context.Context, userID string) error
	ResetPassword(ctx context.Context, userID, password string) error
	GrantAdmin(ctx context.Context, email string) error
}

// AttachAccounts 注入账号体系服务(nil 或不注入 = 保持 token 粘贴模式)。在 Router() 之前调用。
func (s *Server) AttachAccounts(a *accounts.Service) {
	if a != nil {
		s.accounts = a
	}
}

// mountAccounts 把 authkit 的全部认证路由注册到 gin 引擎。
func (s *Server) mountAccounts(e *gin.Engine) {
	svc, ok := s.accounts.(*accounts.Service)
	if !ok {
		return
	}
	if err := authkitgin.Mount(e, svc.Client); err != nil {
		log.Fatalf("authkit mount: %v", err)
	}
}
