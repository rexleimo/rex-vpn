// billing_routes_test.go: 订阅闭环契约测试(W-C)。stub 收银台签真 RSA 事件,
// 走完整 verify→dedupe→provision→link→mirror 链路, 全程无 PG/网络。
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vpnmaster/user-center/internal/accounts"
	"vpnmaster/user-center/internal/billing"
	"vpnmaster/user-center/internal/marzban"

	waffokit "github.com/rexleimo/waffo-kit"
)

type billingFixture struct {
	srv   http.Handler
	fake  *fakeAccounts
	mz    *marzban.Mock
	store *billing.MemStore
}

func newBillingServer(t *testing.T) *billingFixture {
	t.Helper()
	mz := marzban.NewMock()
	s, err := New(mz, marzban.PlansFromEnv(), false)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeAccounts{
		auth:   accounts.Auth{UserID: "u-123", Email: "rex@dev.local", Username: "rex"},
		links:  map[string]string{},
		email:  "rex@dev.local",
		admins: map[string]bool{"u-123": true},
	}
	s.accounts = f
	kit, err := waffokit.New(waffokit.Settings{Mode: waffokit.ModeStub, PublicBase: "http://pub.test"})
	if err != nil {
		t.Fatal(err)
	}
	store := billing.NewMemStore()
	plans := []marzban.Plan{
		{Name: "月付基础", Price: 2900, Bytes: 50 << 30, Days: 30},
		{Name: "年付畅享", Price: 29900, Bytes: 500 << 30, Days: 365},
	}
	bs := billing.New(kit, mz, f, store, plans, "http://pub.test")
	s.AttachBilling(bs)
	return &billingFixture{srv: s.Router(), fake: f, mz: mz, store: store}
}

func (b *billingFixture) get(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	b.srv.ServeHTTP(rec, req)
	return rec
}

func (b *billingFixture) post(path, form string, cookie *http.Cookie) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", path, strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	b.srv.ServeHTTP(rec, req)
	return rec
}

// 未订阅账号: dashboard 门槛跳 /billing(而非旧 /bind); 导航出现订阅入口。
func TestUnboundGoesToBillingNotBind(t *testing.T) {
	b := newBillingServer(t)
	mux := b.srv
	cookie := acctCookie(t, mux)

	rec := b.get("/dashboard", cookie)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/billing" {
		t.Fatalf("unbound = %d %q want 303 /billing", rec.Code, rec.Header().Get("Location"))
	}
	rec = b.get("/billing", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("billing page = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "月付基础") || !strings.Contains(body, "/billing/checkout") {
		t.Fatalf("billing page missing plans/form: %s", body)
	}
	if !strings.Contains(body, `href="/billing"`) {
		t.Fatal("nav should point to /billing")
	}
	if strings.Contains(body, `href="/purchase"`) {
		t.Fatal("old purchase nav must be swapped when billing on")
	}
}

// 完整闭环: 套餐→stub 收银台→支付→签名 webhook→provision→dashboard/订阅 URL。
func TestFullPurchaseLoop(t *testing.T) {
	b := newBillingServer(t)
	cookie := acctCookie(t, b.srv)

	rec := b.post("/billing/checkout", "plan=月付基础", cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("checkout = %d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "http://pub.test/_waffo/stub/c?sid=") {
		t.Fatalf("checkout url %q", loc)
	}
	// 收银台页(同 path, 换测试主机)
	page := b.get(strings.TrimPrefix(loc, "http://pub.test"), cookie)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "月付基础") {
		t.Fatalf("cashier = %d", page.Code)
	}
	// 支付 → 事件经完整验签链投递(在 confirm 内部 relay 到 /webhooks/waffo)
	sid := loc[strings.Index(loc, "sid=")+4:]
	conf := b.post("/_waffo/stub/confirm", "sid="+sid+"&action=pay", nil)
	if conf.Code != http.StatusOK || strings.Contains(conf.Body.String(), "failed") {
		t.Fatalf("confirm = %d %s", conf.Code, conf.Body.String())
	}

	if len(b.mz.Created) != 1 {
		t.Fatalf("provision calls %d", len(b.mz.Created))
	}
	tok := b.mz.Created[0].Token
	if b.fake.links["u-123"] != tok {
		t.Fatalf("account link: %v", b.fake.links)
	}
	row, found, err := b.store.GetSub(t.Context(), "u-123")
	if !found || err != nil || row.Status != "active" || row.PlanName != "月付基础" {
		t.Fatalf("mirror %+v found=%v err=%v", row, found, err)
	}

	// 有订阅后 dashboard 直通, 订阅页出现 URL
	if rec := b.get("/dashboard", cookie); rec.Code != http.StatusOK {
		t.Fatalf("dashboard after pay = %d loc=%q", rec.Code, rec.Header().Get("Location"))
	}
	rec = b.get("/subscription", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tok) {
		t.Fatalf("subscription page should show token %s", tok)
	}
	if !strings.Contains(rec.Body.String(), "marzban.example/sub/") {
		t.Fatal("subscription URL base missing")
	}

	// 已有生效订阅再下单 → 拒绝(引导管理)
	rec = b.post("/billing/checkout", "plan=年付畅享", cookie)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "err=") {
		t.Fatalf("double subscribe should error, got %d %q", rec.Code, rec.Header().Get("Location"))
	}

	// 续费模拟: renewed 事件延期
	conf = b.post("/_waffo/stub/confirm", "sid="+sid+"&action=renew", nil)
	if conf.Code != http.StatusOK || strings.Contains(conf.Body.String(), "failed") {
		t.Fatalf("renew confirm = %d", conf.Code)
	}
	if len(b.mz.Mods) == 0 {
		t.Fatal("renewal must ModifyUser")
	}
}

// 退款事件 → 断供投影(mirror refunded + Modify expire 归今)。
func TestRefundCutoffViaLoop(t *testing.T) {
	b := newBillingServer(t)
	cookie := acctCookie(t, b.srv)
	rec := b.post("/billing/checkout", "plan=月付基础", cookie)
	sid := rec.Header().Get("Location")
	sid = sid[strings.Index(sid, "sid=")+4:]
	if c := b.post("/_waffo/stub/confirm", "sid="+sid+"&action=pay", nil); c.Code != 200 {
		t.Fatal("pay failed")
	}
	if c := b.post("/_waffo/stub/confirm", "sid="+sid+"&action=refund", nil); c.Code != 200 {
		t.Fatalf("refund = %d", c.Code)
	}
	row, found, _ := b.store.GetSub(t.Context(), "u-123")
	if !found || row.Status != "refunded" {
		t.Fatalf("mirror should be refunded: %+v", row)
	}
	if len(b.mz.Created) == 0 {
		t.Fatal("no created user")
	}
	mod := b.mz.Mods[b.mz.Created[0].Username]
	if mod.Expire == nil {
		t.Fatal("cutoff must set expire")
	}
}

// 伪造签名回调必须 400(公开路由但验签在 kit 内)。
func TestWebhookPublicRouteRejectsForged(t *testing.T) {
	b := newBillingServer(t)
	rec := b.post("/webhooks/waffo", `{"eventId":"X","eventType":"subscription.activated"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("forged webhook = %d, want 400", rec.Code)
	}
}

// billing 未启用: /billing 不存在, 旧 /bind 门槛保留(降级路径)。
func TestBillingDisabledKeepsLegacyGate(t *testing.T) {
	s, _ := newAcctServer(t)
	mux := s.Router()
	cookie := acctCookie(t, mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/dashboard", nil)
	req.AddCookie(cookie)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/bind" {
		t.Fatalf("no-billing unbound = %d %q want /bind", rec.Code, rec.Header().Get("Location"))
	}
	rec = httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/billing", nil)
	req2.AddCookie(cookie)
	mux.ServeHTTP(rec, req2)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("/billing disabled = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

// 未登录访问公开 stub/回调之外的订阅路由 → 303 /login(auth 组)。
func TestBillingRequiresSession(t *testing.T) {
	b := newBillingServer(t)
	rec := b.get("/billing", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("anon /billing = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}
