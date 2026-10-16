// accounts_test.go: 账号模式(WI-3/WI-4)契约测试 — fake backend, 不依赖 PG/authkit。
package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vpnmaster/user-center/internal/accounts"
)

type fakeAccounts struct {
	auth     accounts.Auth
	loginErr error
	links    map[string]string // accountID -> marzban token
	setLink  []string          // 记录 SetLink 调用
	email    string

	// WI-5 管理面
	admins map[string]bool
	listed []accounts.AdminUser
	ops    []string // 记录 create/ban/unban/delete/reset/grant
}

func (f *fakeAccounts) IsAdmin(ctx context.Context, userID string) bool { return f.admins[userID] }
func (f *fakeAccounts) ListAccounts(ctx context.Context, search string) ([]accounts.AdminUser, error) {
	return f.listed, nil
}
func (f *fakeAccounts) CreatePasswordUser(ctx context.Context, email, username, password string) (string, error) {
	f.ops = append(f.ops, "create:"+email)
	return "u-new", nil
}
func (f *fakeAccounts) BanAccount(ctx context.Context, id, reason string) error {
	f.ops = append(f.ops, "ban:"+id)
	return nil
}
func (f *fakeAccounts) UnbanAccount(ctx context.Context, id string) error {
	f.ops = append(f.ops, "unban:"+id)
	return nil
}
func (f *fakeAccounts) DeleteAccount(ctx context.Context, id string) error {
	f.ops = append(f.ops, "delete:"+id)
	return nil
}
func (f *fakeAccounts) ResetPassword(ctx context.Context, id, password string) error {
	f.ops = append(f.ops, "resetpw:"+id)
	return nil
}
func (f *fakeAccounts) GrantAdmin(ctx context.Context, email string) error {
	f.ops = append(f.ops, "grant:"+email)
	return nil
}

func (f *fakeAccounts) Login(ctx context.Context, identifier, password, ip string) (accounts.Auth, error) {
	if f.loginErr != nil {
		return accounts.Auth{}, f.loginErr
	}
	return f.auth, nil
}
func (f *fakeAccounts) LinkToken(ctx context.Context, accountID string) (string, bool, error) {
	tok, ok := f.links[accountID]
	return tok, ok, nil
}
func (f *fakeAccounts) SetLink(ctx context.Context, accountID, token string) error {
	f.links[accountID] = token
	f.setLink = append(f.setLink, accountID+"="+token)
	return nil
}
func (f *fakeAccounts) AccountEmail(ctx context.Context, accountID string) (string, error) {
	return f.email, nil
}

func newAcctServer(t *testing.T) (*Server, *fakeAccounts) {
	t.Helper()
	s := newServer(t)
	f := &fakeAccounts{
		auth:   accounts.Auth{UserID: "u-123", Email: "rex@dev.local", Username: "rex"},
		links:  map[string]string{},
		email:  "rex@dev.local",
		admins: map[string]bool{"u-123": true},
	}
	s.accounts = f
	return s, f
}

func jsonReq(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-WM-Fetch", "json")
	return req
}

func acctCookie(t *testing.T, mux http.Handler) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, jsonReq("POST", "/login", "identifier=rex&password=pw"))
	if rec.Code != http.StatusOK {
		t.Fatalf("acct login = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if b := rec.Body.String(); !strings.Contains(b, `"redirect":"/dashboard"`) {
		t.Fatalf("acct login body = %s", b)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

// 登录页双模式: 账号=邮箱密码表单, 不含 token 输入; legacy 保持原样。
func TestLoginPageModes(t *testing.T) {
	s, _ := newAcctServer(t)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest("GET", "/login", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `name="identifier"`) || !strings.Contains(body, `type="password"`) {
		t.Fatalf("accounts login page missing email/password fields")
	}
	if strings.Contains(body, `placeholder="订阅 token"`) {
		t.Fatalf("accounts login page must not show token paste")
	}

	leg := newServer(t)
	rec = httptest.NewRecorder()
	leg.Router().ServeHTTP(rec, httptest.NewRequest("GET", "/login", nil))
	if !strings.Contains(rec.Body.String(), `name="token"`) {
		t.Fatalf("legacy login page regressed")
	}
}

// 登录失败: 统一话术, 不泄露账号存在性; JSON 模式 401。
func TestAcctLoginFailure(t *testing.T) {
	s, f := newAcctServer(t)
	f.loginErr = errors.New("bogus credential")
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, jsonReq("POST", "/login", "identifier=nobody&password=x"))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "账号或密码错误") {
		t.Fatalf("fail login = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "bogus") {
		t.Fatal("internal error leaked to client")
	}
}

// 已登录未绑定 → /bind; 绑定后 → dashboard 200; 绑定 POST 校验 token 并写映射。
func TestAcctBindFlow(t *testing.T) {
	s, f := newAcctServer(t)
	mux := s.Router()
	cookie := acctCookie(t, mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/dashboard", nil)
	req.AddCookie(cookie)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/bind" {
		t.Fatalf("unbound dashboard = %d %q, want 303 /bind", rec.Code, rec.Header().Get("Location"))
	}

	rec = httptest.NewRecorder()
	req = jsonReq("POST", "/bind", "token=test-token")
	req.AddCookie(cookie)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || len(f.setLink) != 1 {
		t.Fatalf("bind = %d %s links=%v", rec.Code, rec.Body.String(), f.setLink)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/dashboard", nil)
	req.AddCookie(cookie)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bound dashboard = %d, want 200", rec.Code)
	}

	// 绑定页可重复访问(门槛已拆环): GET /bind 渲染账号邮箱
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/bind", nil)
	req.AddCookie(cookie)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "rex@dev.local") {
		t.Fatalf("bind page = %d, want 200 with account email", rec.Code)
	}
}

// 账号模式换 token: 写映射表, 不重种会话 cookie。
func TestAcctRetokenKeepsSession(t *testing.T) {
	s, f := newAcctServer(t)
	f.links["u-123"] = "test-token"
	mux := s.Router()
	cookie := acctCookie(t, mux)

	rec := httptest.NewRecorder()
	req := jsonReq("POST", "/subscription/token", "token=test-token")
	req.AddCookie(cookie)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("retoken = %d %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			t.Fatal("retoken must not re-issue session cookie in accounts mode")
		}
	}
	if len(f.setLink) != 1 {
		t.Fatalf("SetLink calls = %v", f.setLink)
	}
}

// ---- WI-5: 用户管理页 ----

func postJSON(t *testing.T, mux http.Handler, target, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := jsonReq("POST", target, body)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAdminUsersGateAndActions(t *testing.T) {
	s, f := newAcctServer(t)
	mux := s.Router()
	cookie := acctCookie(t, mux)

	// 非 admin → 踢回 dashboard
	f.admins["u-123"] = false
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin/users", nil)
	req.AddCookie(cookie)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dashboard" {
		t.Fatalf("non-admin /admin/users = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	f.admins["u-123"] = true

	// admin → 200 + 行渲染 + 导航入口
	f.listed = []accounts.AdminUser{
		{ID: "u-123", Email: "rex@dev.local", Status: "active", Admin: true},
		{ID: "u-9", Email: "new@x.test", Username: "newuser", Status: "banned"},
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/admin/users", nil)
	req.AddCookie(cookie)
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "rex@dev.local") || !strings.Contains(body, "new@x.test") {
		t.Fatalf("admin page = %d, render missing users", rec.Code)
	}
	if !strings.Contains(body, "已禁用") {
		t.Fatal("banned badge missing")
	}

	// 添加用户
	if r := postJSON(t, mux, "/admin/users/create", "email=a@x.test&password=12345678", cookie); r.Code != http.StatusOK {
		t.Fatalf("create = %d %s", r.Code, r.Body.String())
	}
	// 密码过短拒绝
	if r := postJSON(t, mux, "/admin/users/create", "email=a@x.test&password=1", cookie); r.Code != http.StatusBadRequest {
		t.Fatalf("short-pw create = %d, want 400", r.Code)
	}

	// 自操作保护
	r := postJSON(t, mux, "/admin/users/ban", "id=u-123", cookie)
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "不能对自己") {
		t.Fatalf("self-ban = %d %s", r.Code, r.Body.String())
	}
	// 正常禁用
	if r := postJSON(t, mux, "/admin/users/ban", "id=u-9", cookie); r.Code != http.StatusOK {
		t.Fatalf("ban = %d %s", r.Code, r.Body.String())
	}
	want := map[string]bool{"ban:u-9": true}
	for _, op := range f.ops {
		delete(want, op)
	}
	if len(want) != 0 {
		t.Fatalf("ops = %v, want ban:u-9", f.ops)
	}
}

// 导航: admin 见「用户管理」, 普通用户不见。
func TestNavAdminEntry(t *testing.T) {
	s, f := newAcctServer(t)
	f.links["u-123"] = "test-token"
	mux := s.Router()
	cookie := acctCookie(t, mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/dashboard", nil)
	req.AddCookie(cookie)
	mux.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "/admin/users") {
		t.Fatal("admin nav entry missing")
	}

	f.admins["u-123"] = false
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/dashboard", nil)
	req.AddCookie(cookie)
	mux.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "/admin/users") {
		t.Fatal("non-admin must not see admin nav")
	}
}
