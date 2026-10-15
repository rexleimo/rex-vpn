// Package web 是 user-center 的 SSR 页面 + JSON API(work-10 / wc-2..5)。
// 不做前后分离: 服务端渲染 4 页, vanilla JS 5s 轮询 JSON 端点刷新。
// 鉴权: token 登录 → 服务端经 Marzban 校验后种 HttpOnly cookie 会话; token 只在 cookie, 页面不渲染明文。
package web

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"sync"
	"time"

	qr "github.com/skip2/go-qrcode"
	"vpnmaster/user-center/internal/marzban"
	"vpnmaster/webui"
)

//go:embed uc.css
var ucCSS string

const cookieName = "uc_sess"

// Server 是 user-center 的 handler 集合。
type Server struct {
	Marzban     marzban.Client
	Plans       []marzban.Plan
	PlansByName map[string]marzban.Plan
	Template    *template.Template

	secureCookie bool // 线上 HTTPS 置 true

	mu       sync.Mutex
	sessions map[string]string     // sessionID -> token
	cache    map[string]cacheEntry // token -> 最近一次解析(30s)
	// 登录限流: IP -> 时间戳窗口
	rateMu sync.Mutex
	rate   map[string][]time.Time
}

type cacheEntry struct {
	at   time.Time
	user marzban.User
	err  error
}

// New 构造; 依赖注入 marzban client(可 Mock) 与 plans。
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
		Template: tpl, secureCookie: secure,
		sessions: map[string]string{}, cache: map[string]cacheEntry{},
		rate: map[string][]time.Time{},
	}, nil
}

// Router 挂载全部路由。
func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /", s.root)
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginPost)
	mux.HandleFunc("POST /logout", s.logout)

	mux.HandleFunc("GET /dashboard", s.auth(s.dashboard))
	mux.HandleFunc("GET /subscription", s.auth(s.subscription))
	mux.HandleFunc("POST /subscription/token", s.auth(s.retoken))
	mux.HandleFunc("GET /purchase", s.auth(s.purchase))
	mux.HandleFunc("POST /purchase", s.auth(s.createInvoice))

	mux.HandleFunc("GET /api/status", s.auth(s.apiStatus))
	mux.HandleFunc("GET /api/invoices", s.auth(s.apiInvoices))
	mux.HandleFunc("GET /qr.png", s.auth(s.qrPNG))

	return securityHeaders(mux)
}

// ---- 会话 ----

func (s *Server) issue(w http.ResponseWriter, token string) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	sid := hex.EncodeToString(b)
	s.mu.Lock()
	s.sessions[sid] = token
	s.mu.Unlock()
	c := &http.Cookie{
		Name:     cookieName,
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((72 * time.Hour).Seconds()),
	}
	if s.secureCookie {
		c.Secure = true
	}
	http.SetCookie(w, c)
}

func (s *Server) currentToken(r *http.Request) (string, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return "", false
	}
	s.mu.Lock()
	tok, ok := s.sessions[c.Value]
	s.mu.Unlock()
	return tok, ok
}

// userFor 取会话 token 对应的用户(30s 缓存, 避免 5s 轮询打爆 Marzban)。
func (s *Server) userFor(ctx context.Context, r *http.Request) (marzban.User, bool, error) {
	tok, ok := s.currentToken(r)
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
	if err != nil {
		return u, true, err
	}
	return u, true, nil
}

// auth 会话门槛: 无会话 302 → /login; token 在 Marzban 失效也踢回登录。
func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, _, err := s.userFor(r.Context(), r)
		if err != nil || u.Username == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		r = withUser(r, u)
		h(w, r)
	}
}

type ctxKey int

const userKey ctxKey = iota

func withUser(r *http.Request, u marzban.User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userKey, u))
}

func userFrom(r *http.Request) marzban.User {
	if u, ok := r.Context().Value(userKey).(marzban.User); ok {
		return u
	}
	return marzban.User{}
}

// ---- 路由处理 ----

func (s *Server) root(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.currentToken(r); ok {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	s.page(w, Page{Title: "登录", Content: "login",
		PageData: map[string]any{"Err": r.URL.Query().Get("err") != ""}})
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	// wc-5.1: 每 IP 10 次/分 登录限流
	ip := clientIP(r)
	if !s.rateAllow(ip) {
		http.Redirect(w, r, "/login?err=限流,请稍后再试", http.StatusSeeOther)
		return
	}
	token := r.FormValue("token")
	u, err := s.Marzban.GetUserByToken(r.Context(), token)
	if err != nil || u.Username == "" {
		http.Redirect(w, r, "/login?err=token无效", http.StatusSeeOther)
		return
	}
	s.clearSession(w)
	s.issue(w, token)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.clearSession(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", MaxAge: -1, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

// ---- SSR 页 ----

func (s *Server) page(w http.ResponseWriter, p Page) {
	p.CSS = webui.CSS() + ucCSS
	webui.WritePage(w, s.Template, "layout", p)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
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
	d.ExpireStr = fmtExpireDays(u.ExpireDays)
	s.page(w, Page{Title: "概览", Active: "dashboard", Content: "pageDashboard", PageData: d})
}

func (s *Server) subscription(w http.ResponseWriter, r *http.Request) {
	token, _ := s.currentToken(r)
	subURL := s.Marzban.SubURL(token)
	var d SubData
	d.SubURL = subURL
	d.Changed = r.URL.Query().Get("changed") != ""
	d.Err = r.URL.Query().Get("err")
	d.Guides = []Guide{
		{Title: "Clash Verge", Desc: "导入订阅链接(右键 → 复制 → 添加订阅)"},
		{Title: "Stash", Desc: "配置 → 添加远程配置 → 粘贴订阅链接"},
		{Title: "Windows 客户端(Phase 2)", Desc: "即将上线, 一键导入"},
	}
	s.page(w, Page{Title: "订阅", Active: "subscription", Content: "pageSubscription", PageData: d})
}

// retoken wc-3.3: 更换 token(Marzban regenerate 后重粘) → 重校验 + 重种 cookie。
func (s *Server) retoken(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	if u, err := s.Marzban.GetUserByToken(r.Context(), token); err == nil && u.ID != 0 {
		s.clearSession(w)
		s.issue(w, token)
		http.Redirect(w, r, "/subscription?changed=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/subscription?err=token无效", http.StatusSeeOther)
}

func (s *Server) purchase(w http.ResponseWriter, r *http.Request) {
	token, _ := s.currentToken(r)
	invs, _ := s.Marzban.ListInvoices(r.Context(), token)
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
	d.JustOrdered = r.URL.Query().Get("new") != ""
	d.Err = r.URL.Query().Get("err")
	s.page(w, Page{Title: "购买", Active: "purchase", Content: "pagePurchase", PageData: d})
}

func (s *Server) createInvoice(w http.ResponseWriter, r *http.Request) {
	plan := r.FormValue("plan")
	if _, ok := s.PlansByName[plan]; !ok {
		http.Redirect(w, r, "/purchase?err=无效套餐", http.StatusSeeOther)
		return
	}
	token, _ := s.currentToken(r)
	if _, err := s.Marzban.CreateInvoice(r.Context(), token, plan); err != nil {
		http.Redirect(w, r, "/purchase?err=下单失败,请稍后重试", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/purchase?new=1", http.StatusSeeOther)
}

// ---- JSON API(轮询) ----

func (s *Server) apiStatus(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	// 刷新缓存: 轮询直接取最近一次 userFor 结果; 超过 30s 重新解析
	fresh, _, _ := s.userFor(r.Context(), r)
	if fresh.Username != "" {
		u = fresh
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username": u.Username, "status": u.Status, "online": u.Online,
		"expire": fmtExpireDays(u.ExpireDays),
		"used":   humanBytes(u.UsedTraffic), "limit": humanBytes(u.LimitTraffic),
	})
}

func (s *Server) apiInvoices(w http.ResponseWriter, r *http.Request) {
	token, _ := s.currentToken(r)
	invs, err := s.Marzban.ListInvoices(r.Context(), token)
	if err != nil {
		writeJSON(w, http.StatusOK, []InvoiceView{})
		return
	}
	views := make([]InvoiceView, 0, len(invs))
	for _, i := range invs {
		views = append(views, InvoiceView{
			ID: i.ID, PlanName: i.PlanName, Amount: i.Amount, Status: i.Status,
			CreatedAt: time.Unix(i.CreatedAt, 0).Format("2006-01-02 15:04"),
		})
	}
	writeJSON(w, http.StatusOK, views)
}

// qrPNG wc-3.1: 服务端出二维码(go-qrcode), img-src 'self' 友好。
func (s *Server) qrPNG(w http.ResponseWriter, r *http.Request) {
	subURL := s.Marzban.SubURL(func() string {
		t, _ := s.currentToken(r)
		return t
	}())
	png, err := qrcodePNG(subURL, 256)
	if err != nil {
		http.Error(w, "qr", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png)
}

// qrPNG wc-3.1: 服务端出二维码(go-qrcode), img-src 'self' 友好。
func qrcodePNG(content string, size int) ([]byte, error) {
	return qr.Encode(content, qr.Medium, size)
}

// ---- 安全辅助 ----

// securityHeaders wc-5.2: CSP 默认 deny+self + 安全头。
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self'; object-src 'none'; frame-ancestors 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) rateAllow(ip string) bool {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	now := time.Now()
	var keeps []time.Time
	for _, t := range s.rate[ip] {
		if now.Sub(t) < time.Minute {
			keeps = append(keeps, t)
		}
	}
	keeps = append(keeps, now)
	if len(keeps) > 10 {
		s.rate[ip] = keeps
		return false
	}
	s.rate[ip] = keeps
	return true
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	return r.RemoteAddr
}

// ---- 视图 ----

type Page struct {
	Title, Active string
	CSS           string
	Content       string
	PageData      any
}

type DashboardData struct {
	Username  string
	Status    string
	Online    bool
	UsedStr   string
	LimitStr  string
	QuotaPct  int
	ExpireStr string
}

type Guide struct {
	Title string
	Desc  string
}

type SubData struct {
	SubURL  string
	Guides  []Guide
	Changed bool
	Err     string
}

type PlanView struct {
	Name  string
	Price int64
	GBStr string
	Days  int
}

type PurchaseData struct {
	Plans       []PlanView
	Invoices    []marzban.Invoice
	JustOrdered bool
	Err         string
}

type InvoiceView struct {
	ID        int64
	PlanName  string
	Amount    int64
	Status    string
	CreatedAt string
}

func humanBytes(b int64) string {
	if b <= 0 {
		return "0 B"
	}
	const unit = 1024
	i := 0
	f := float64(b)
	for f >= unit && i < 5 {
		f /= unit
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", b)
	}
	return fmt.Sprintf("%.1f %ciB", f, "KMGTPE"[i-1])
}

func fmtExpireDays(days int) string {
	if days <= 0 {
		return "不限时"
	}
	return fmt.Sprintf("%d 天", days)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}
