package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"vpnmaster/ingest/internal/db"
	"vpnmaster/ingest/internal/domain"
)

func newHandler(t *testing.T) (*Handler, *db.Store) {
	t.Helper()
	store, err := db.NewStore("sqlite", filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	h, err := NewHandler(store, nil, nil, nil, nil, NewSession("secret"))
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h, store
}

func login(t *testing.T, h *Handler) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/login", strings.NewReader("password=secret"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login POST = %d, want 302", rec.Code)
	}
	return rec
}

func withCookie(t *testing.T, rec *httptest.ResponseRecorder) http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName {
			return *c
		}
	}
	t.Fatal("no session cookie issued")
	return http.Cookie{}
}

// 7.1 验收: 未登录 302 → /login; 登录后 5 页 200。
func TestPagesAuthAndRender(t *testing.T) {
	h, _ := newHandler(t)
	mux := h.Router()

	// 未登录: 各管理页 302 → /login
	for _, p := range []string{"/admin", "/admin/upstreams", "/admin/nodes", "/admin/events", "/admin/users"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != http.StatusSeeOther {
			t.Errorf("%s unauthed = %d, want 302", p, rec.Code)
		}
		if rec.Header().Get("Location") != "/login" {
			t.Errorf("%s location = %q, want /login", p, rec.Header().Get("Location"))
		}
	}

	// 错误口令 → 回 /login?err=1
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/login", strings.NewReader("password=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login?err=1" {
		t.Fatalf("bad login = %d %q", rec.Code, rec.Header().Get("Location"))
	}

	// 正确登录 → 302 /admin + 会话 cookie(HttpOnly)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/login", strings.NewReader("password=secret"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin" {
		t.Fatalf("good login = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	cookie := withCookie(t, rec)
	if !cookie.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}

	// 登录后 5 页 200
	for _, p := range []string{"/admin", "/admin/upstreams", "/admin/nodes", "/admin/events", "/admin/users"} {
		r := httptest.NewRequest("GET", p, nil)
		r.AddCookie(&cookie)
		pr := httptest.NewRecorder()
		mux.ServeHTTP(pr, r)
		if pr.Code != http.StatusOK {
			t.Fatalf("%s authed = %d, want 200\n%s", p, pr.Code, pr.Body.String()[:min(300, pr.Body.Len())])
		}
	}
}

// 7.2 验收: 上游 CRUD + 节点摘除/恢复走查。
func TestAdminOperations(t *testing.T) {
	h, store := newHandler(t)
	mux := h.Router()
	cookie := withCookie(t, login(t, h))

	// 添加上游
	req := httptest.NewRequest("POST", "/admin/upstreams", strings.NewReader("name=vpn4zx&url=https://sub.example.com/v?token=abcd"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("add upstream = %d", rec.Code)
	}
	ups, _ := store.ListUpstreams(context.Background())
	if len(ups) != 1 || ups[0].Name != "vpn4zx" {
		t.Fatalf("upstreams = %+v", ups)
	}
	if strings.Contains(ups[0].URLMasked, "token=abcd") {
		t.Fatal("URL must be masked in DB")
	}

	// 造一个 active 节点 → 摘除 → 恢复
	n := domain.Node{Address: "9.9.9.9", Port: 443, Protocol: domain.ProtoVLess,
		Stream: domain.Stream{User: "u", Network: "tcp", SNI: "x", StreamJSON: `{"network":"tcp","user":"u","sni":"x"}`}}
	if _, _, err := store.UpsertNodes(context.Background(), "vpn4zx", []domain.Node{n}); err != nil {
		t.Fatal(err)
	}
	fp := domain.NewFingerprint(n)
	_ = store.SetNodeStatus(context.Background(), fp, string(domain.NodePending), string(domain.NodeActive))

	req = httptest.NewRequest("POST", "/admin/nodes/evict", strings.NewReader("fp="+fp))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&cookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	ev, _ := store.ListNodes(context.Background(), string(domain.NodeEvicted))
	if len(ev) != 1 {
		t.Fatalf("after evict: evicted=%d, want 1", len(ev))
	}

	req = httptest.NewRequest("POST", "/admin/nodes/recover", strings.NewReader("fp="+fp))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&cookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	acts, _ := store.ListNodes(context.Background(), string(domain.NodeActive))
	if len(acts) != 1 {
		t.Fatalf("after recover: active=%d, want 1", len(acts))
	}
}

// 7.4 验收: JSON API 契约。
func TestAPIContracts(t *testing.T) {
	h, store := newHandler(t)
	mux := h.Router()

	// /api/healthz 开放
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d", rec.Code)
	}
	var hz map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&hz); err != nil || hz["status"] != "ok" {
		t.Fatalf("healthz body = %s", rec.Body.String())
	}

	cookie := withCookie(t, login(t, h))

	// /api/upstreams 契约
	req := httptest.NewRequest("GET", "/api/upstreams", nil)
	req.AddCookie(&cookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("api/upstreams = %d", rec.Code)
	}
	var upArr []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&upArr); err != nil {
		t.Fatalf("api/upstreams not json array: %v (%s)", err, rec.Body.String())
	}

	// /api/nodes 契约: 先造节点
	n := domain.Node{Address: "1.1.1.1", Port: 443, Protocol: domain.ProtoVLess,
		Stream: domain.Stream{User: "u", Network: "tcp", SNI: "x", StreamJSON: `{"network":"tcp","user":"u"}`}}
	_, _, _ = store.UpsertNodes(context.Background(), "s", []domain.Node{n})
	req = httptest.NewRequest("GET", "/api/nodes", nil)
	req.AddCookie(&cookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var nodes []NodeView
	if err := json.NewDecoder(rec.Body).Decode(&nodes); err != nil || len(nodes) != 1 {
		t.Fatalf("api/nodes = %v (%s)", err, rec.Body.String())
	}
	if nodes[0].Addr != "1.1.1.1" || nodes[0].Status != string(domain.NodePending) {
		t.Fatalf("node view = %+v", nodes[0])
	}

	// 未登录 /api/nodes = 401
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/nodes", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("api/nodes unauthed = %d, want 401", rec.Code)
	}
}

// 组件渲染快照(webui): layout 完整 + 关键组件存在。
func TestWebUISnapshot(t *testing.T) {
	h, _ := newHandler(t)
	cookie := withCookie(t, login(t, h))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin", nil)
	req.AddCookie(&cookie)
	h.Router().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{
		"<title>总览 · vpn-master</title>",
		"wm-nav", "wm-card", "wm-stat-num", "总览",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("snapshot missing %q", want)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
