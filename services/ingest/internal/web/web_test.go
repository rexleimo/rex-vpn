package web

import (
	"context"
	"encoding/json"
	"fmt"
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
		if c.Name == adminCookie {
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
		"<title>总览 · vpn-master 管理</title>",
		`href="/assets/bootstrap.min.css"`, "navbar", "wm-glass",
		`src="/assets/wm-ui.js"`, `id="wm-toasts"`, `id="wm-confirm"`,
		"wm-card", "wm-stat-num", "总览", // 未迁移页过渡样式层仍在
	} {
		if !strings.Contains(body, want) {
			t.Errorf("snapshot missing %q", want)
		}
	}
}

// S1: 上游页 CRUD 契约(编辑弹窗/确认删除/JSON 协商)。
func TestUpstreamCRUDContract(t *testing.T) {
	h, store := newHandler(t)
	mux := h.Router()
	cookie := withCookie(t, login(t, h))

	// 预置一行上游, 保证删除确认表单渲染进断言范围
	if _, err := store.AddUpstream(context.Background(), "aaa", "https://x.example.com/s?t=1"); err != nil {
		t.Fatal(err)
	}
	// 页面 DOM: 编辑模态 + 删除确认绑定 + update 端点表单
	req := httptest.NewRequest("GET", "/admin/upstreams", nil)
	req.AddCookie(&cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{`id="wm-edit"`, "/admin/upstreams/update", "wmConfirmAct", "wmOpenEdit"} {
		if !strings.Contains(body, want) {
			t.Errorf("upstreams page missing %q", want)
		}
	}

	// 添加(JSON 模式)
	post := func(path string, form string, jsonMode bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(form))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if jsonMode {
			r.Header.Set("X-WM-Fetch", "json")
		}
		r.AddCookie(&cookie)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	rec = post("/admin/upstreams", "name=bbb&url=https://x.example.com/s?t=1", true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("add json = %d %s", rec.Code, rec.Body.String())
	}
	// 重复添加 → 409 {ok:false}
	rec = post("/admin/upstreams", "name=aaa&url=https://x.example.com/s?t=2", true)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"ok":false`) {
		t.Fatalf("dup add json = %d %s, want 409 ok:false", rec.Code, rec.Body.String())
	}
	// 编辑 URL → ok, 掩码更新
	rec = post("/admin/upstreams/update", "name=aaa&url=https://y.example.com/s?t=secret9", true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("update json = %d %s", rec.Code, rec.Body.String())
	}
	ups, _ := store.ListUpstreams(context.Background())
	if len(ups) != 2 || !strings.Contains(ups[0].URLMasked, "y.example.com") || strings.Contains(ups[0].URLMasked, "secret9") {
		t.Fatalf("after update masked = %+v", ups)
	}
	// 编辑不存在的来源 → 404
	rec = post("/admin/upstreams/update", "name=nope&url=https://z/", true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("update missing = %d, want 404", rec.Code)
	}
	// 无 JS 表单路径仍 303(渐进增强兜底)
	rec = post("/admin/upstreams/update", "name=aaa&url=https://w.example.com/s", false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("form update = %d, want 303", rec.Code)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// S2/ADR-0004: 节点服务端搜索/过滤/分页 + 事件游标; 无参=全量旧契约保持。
func TestNodesPaginationAndEventsCursor(t *testing.T) {
	h, store := newHandler(t)
	mux := h.Router()
	cookie := withCookie(t, login(t, h))
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := store.UpsertRow(ctx, &db.NodeRow{
			Fingerprint: fmt.Sprintf("fp%032d", i), Address: fmt.Sprintf("10.0.0.%d", i),
			Port: 443, Protocol: "vless", Status: "active", Tag: fmt.Sprintf("tag-%d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.UpsertRow(ctx, &db.NodeRow{Fingerprint: "deadbeefdeadbeefdeadbeefdeadbeef", Address: "1.2.3.4", Port: 80, Status: "evicted"}); err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.AddCookie(&cookie)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	// 无参 = 全量数组(旧脚本契约), X-Total-Count=6
	rec := get("/api/nodes")
	var all []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil {
		t.Fatalf("unparam /api/nodes must stay array: %v", err)
	}
	if len(all) != 6 || rec.Header().Get("X-Total-Count") != "6" {
		t.Fatalf("all = %d total=%s, want 6/6", len(all), rec.Header().Get("X-Total-Count"))
	}
	// 分页: size=2 page=2 → 2 行, total=6
	rec = get("/api/nodes?size=2&page=2")
	var pg []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &pg)
	if len(pg) != 2 || rec.Header().Get("X-Total-Count") != "6" {
		t.Fatalf("page2 = %d rows total=%s, want 2/6", len(pg), rec.Header().Get("X-Total-Count"))
	}
	// 状态过滤
	rec = get("/api/nodes?status=evicted")
	json.Unmarshal(rec.Body.Bytes(), &pg)
	if len(pg) != 1 || pg[0]["Status"] != "evicted" {
		t.Fatalf("status filter = %+v", pg)
	}
	// 搜索(地址)
	rec = get("/api/nodes?q=10.0.0.3")
	json.Unmarshal(rec.Body.Bytes(), &pg)
	if len(pg) != 1 {
		t.Fatalf("q search = %+v, want 1", pg)
	}
	// 事件游标: 插 60 条, before=末 id 取 50 → 无重叠
	for i := 0; i < 60; i++ {
		if err := store.RecordEvent(ctx, db.Event{Kind: "evict", NodeFP: "x", Detail: fmt.Sprintf("d%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	rec = get("/api/events?limit=50")
	var evs []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &evs)
	if len(evs) != 50 {
		t.Fatalf("events page1 = %d, want 50", len(evs))
	}
	lastID := int64(evs[49]["ID"].(float64))
	rec = get(fmt.Sprintf("/api/events?before=%d&limit=50", lastID))
	json.Unmarshal(rec.Body.Bytes(), &evs)
	if len(evs) != 10 {
		t.Fatalf("events page2 = %d, want 10", len(evs))
	}
	for _, e := range evs {
		if int64(e["ID"].(float64)) >= lastID {
			t.Fatal("cursor overlap")
		}
	}
}
