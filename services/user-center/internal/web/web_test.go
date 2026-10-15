package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vpnmaster/user-center/internal/marzban"
)

func newServer(t *testing.T) *Server {
	t.Helper()
	mock := marzban.NewMock()
	s, err := New(mock, marzban.PlansFromEnv(), false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func loginCookie(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	mux := s.Router()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/login", strings.NewReader("token=test-token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login = %d, want 302", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName && c.Value != "" { // 取真正的会话 cookie(非 clearSession 的空 cookie)
			if !c.HttpOnly {
				t.Error("session cookie must be HttpOnly (wc-5.1)")
			}
			return c
		}
	}
	t.Fatal("no session cookie issued")
	return nil
}

// 假 token 拒绝(wc-2.1 验收)。
func TestLoginRejectsBadToken(t *testing.T) {
	s := newServer(t)
	mux := s.Router()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/login", strings.NewReader("token=nope"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "err") {
		t.Fatalf("bad token login = %d %q, want redirect to /login?err", rec.Code, rec.Header().Get("Location"))
	}
}

// 未登录 302 → /login; 登录后 4 页 200 + 关键 DOM(wc-6.1 验收)。
func TestPagesAuthAndRender(t *testing.T) {
	s := newServer(t)
	mux := s.Router()

	for _, p := range []string{"/dashboard", "/subscription", "/purchase"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != http.StatusSeeOther {
			t.Errorf("%s unauthed = %d, want 302", p, rec.Code)
		}
	}

	cookie := loginCookie(t, s)
	for _, p := range []string{"/dashboard", "/subscription", "/purchase", "/login"} {
		req := httptest.NewRequest("GET", p, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s authed = %d, want 200\n%s", p, rec.Code, rec.Body.String()[:min(len(rec.Body.String()), 300)])
		}
	}
	// 关键 DOM 片段
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/subscription", nil)
	req.AddCookie(cookie)
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{`<input class="wm-input" id="suburl"`, "订阅二维码", "/qr.png", "更换 token"} {
		if !strings.Contains(body, want) {
			t.Errorf("subscription page missing %q", want)
		}
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/purchase", nil)
	req.AddCookie(cookie)
	mux.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "下单") {
		t.Error("purchase page missing 下单 button")
	}
}

// 会话保护: 未登录 /api/* = 302(wc-5.1 会话门槛)。
func TestAPIRequiresSession(t *testing.T) {
	s := newServer(t)
	mux := s.Router()
	for _, p := range []string{"/api/status", "/api/invoices", "/qr.png"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != http.StatusSeeOther {
			t.Errorf("%s unauthed = %d, want 302", p, rec.Code)
		}
	}
}

// 安全头(wc-5.2 验收): CSP 默认 deny + self。
func TestSecurityHeaders(t *testing.T) {
	s := newServer(t)
	mux := s.Router()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/login", nil))
	if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") {
		t.Errorf("CSP = %q, want default-src 'self'", got)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing X-Content-Type-Options")
	}
}

// 登录限流(wc-5.1 验收): 连续 11 次登录, 第 11 次被限流。
func TestLoginRateLimit(t *testing.T) {
	s := newServer(t)
	mux := s.Router()
	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/login", strings.NewReader("token=test-token"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "1.2.3.4:5678"
		mux.ServeHTTP(rec, req)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/login", strings.NewReader("token=test-token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "1.2.3.4:5678"
	mux.ServeHTTP(rec, req)
	loc := rec.Header().Get("Location")
	// 限流时 Location = /login?err=限流 (URL 编码); 有效 token 只有限流路径会 err
	if !strings.Contains(loc, "err=") {
		t.Errorf("11th login not rate-limited: Location=%q", loc)
	}
}

// token 不渲染明文(wc-5.2 验收): 概览/购买页正文不含登录 token。
// 注: /subscription 页展示的订阅链接天然含 token(Marzban sub URL 即凭证), 属有意为之。
func TestTokenNotRendered(t *testing.T) {
	s := newServer(t)
	cookie := loginCookie(t, s)
	mux := s.Router()
	for _, p := range []string{"/dashboard", "/purchase"} {
		req := httptest.NewRequest("GET", p, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if strings.Contains(rec.Body.String(), "test-token") {
			t.Errorf("%s rendered raw token", p)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
