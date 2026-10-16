package waffokit

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- test doubles -----------------------------------------------------------

type memStore struct {
	mu   sync.Mutex
	seen map[string]string
}

func newMemStore() *memStore { return &memStore{seen: map[string]string{}} }

func (m *memStore) Seen(id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.seen[id]
	return ok, nil
}

func (m *memStore) MarkSeen(id, typ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seen[id] = typ
	return nil
}

func collect() (*memStore, func(context.Context, *Event) error, *[]*Event) {
	st := newMemStore()
	var mu sync.Mutex
	var got []*Event
	var fail error
	fn := func(_ context.Context, ev *Event) error {
		mu.Lock()
		defer mu.Unlock()
		if fail != nil {
			return fail
		}
		got = append(got, ev)
		return nil
	}
	return st, fn, &got
}

func newStubKit(t *testing.T) *Kit {
	t.Helper()
	k, err := New(Settings{Mode: ModeStub, PublicBase: "http://site.test", Currency: "CNY"})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// --- stub end-to-end: full verify→dedupe→dispatch path ----------------------

func TestStubPayRenewRefund(t *testing.T) {
	k := newStubKit(t)
	store, fn, events := collect()
	h, err := k.WebhookHandler(WebhookDeps{Store: store, Handle: fn})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/_waffo/stub/", k.StubHandler("/_waffo/stub"))
	mux.Handle("/webhooks/waffo", h)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := k.CreateCheckout(context.Background(), CheckoutRequest{
		ProductID: "PROD_MONTH", BuyerIdentity: "acct:abc", ExternalID: ExternalIDFor("uc_sub", "abc"),
		BuyerEmail: "u@x.test", StubHint: StubHint{PlanName: "月付基础", Price: "¥29", PeriodDays: 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.CheckoutURL, "http://site.test/_waffo/stub/c?sid=") {
		t.Fatalf("stub url: %s", res.CheckoutURL)
	}
	// Cashier page renders through the real mount (PublicBase differs from
	// srv; only the path matters for the handler).
	form := strings.TrimPrefix(res.CheckoutURL, "http://site.test")
	resp, err := http.Get(srv.URL + form)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "月付基础") {
		t.Fatalf("cashier page missing plan: %s", page)
	}
	sid := form[strings.Index(form, "sid=")+4:]

	confirm := func(action string) {
		t.Helper()
		r2, err := http.Post(srv.URL+"/_waffo/stub/confirm", "application/x-www-form-urlencoded",
			strings.NewReader("sid="+sid+"&action="+action))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r2.Body)
		r2.Body.Close()
		if r2.StatusCode != 200 || strings.Contains(string(b), "failed") {
			t.Fatalf("confirm %s: %d %s", action, r2.StatusCode, b)
		}
	}
	confirm("pay")
	confirm("renew")
	confirm("refund")

	var types []string
	for _, ev := range *events {
		types = append(types, ev.Type)
	}
	want := []string{EventSubscriptionActivated, EventSubscriptionRenewed, EventRefundSucceeded}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v want %v", types, want)
	}
	activated := (*events)[0]
	if activated.Data.MerchantProvidedBuyerIdentity == nil || *activated.Data.MerchantProvidedBuyerIdentity != "acct:abc" {
		t.Fatalf("identity not carried: %+v", activated.Data)
	}
	if activated.Data.OrderMerchantExternalID == nil || !strings.HasPrefix(*activated.Data.OrderMerchantExternalID, "uc_sub:") {
		t.Fatalf("ext id not carried: %+v", activated.Data)
	}
	if activated.Data.CurrentPeriodEnd == nil {
		t.Fatal("no period end")
	}
	end1 := *activated.Data.CurrentPeriodEnd
	end2 := *(*events)[1].Data.CurrentPeriodEnd
	if end2 <= end1 {
		t.Fatalf("renewal did not roll period: %s -> %s", end1, end2)
	}
	if store.seen[activated.EventID] != EventSubscriptionActivated {
		t.Fatalf("dedupe store: %v", store.seen)
	}
	if len(store.seen) != 3 {
		t.Fatalf("expected 3 distinct eventIds, got %d: %v", len(store.seen), store.seen)
	}
}

func TestStubDuplicateDeliveryAcked(t *testing.T) {
	k := newStubKit(t)
	store, fn, events := collect()
	h, _ := k.WebhookHandler(WebhookDeps{Store: store, Handle: fn})
	_ = h
	// create + pay once, then re-deliver same signed body manually
	res, _ := k.CreateCheckout(context.Background(), CheckoutRequest{
		ProductID: "PROD_M", BuyerIdentity: "acct:z", ExternalID: "uc_sub:z",
	})
	sid := strings.TrimPrefix(res.CheckoutURL, "http://site.test/_waffo/stub/c?sid=")
	k.stub.mu.Lock()
	sess := k.stub.sess[sid]
	k.stub.mu.Unlock()
	body, _ := json.Marshal(k.stubEnvelope(EventSubscriptionActivated, sess.OrderID, sess))
	sig, _ := k.stub.sign(mustTS() + "." + string(body))
	send := func() string {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/waffo", strings.NewReader(string(body)))
		req.Header.Set("X-Waffo-Signature", "t="+mustTS()+",v1="+sig)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if s, ok := out["status"].(string); ok {
			return s
		}
		return rec.Body.String()
	}
	// mustTS drift fix: sign with the same ts we send
	sig, _ = k.stub.sign(tsStatic + "." + string(body))
	if got := send(); got != "processed" {
		t.Fatalf("first delivery: %s", got)
	}
	if got := send(); got != "duplicate" {
		t.Fatalf("second delivery: %s", got)
	}
	if len(*events) != 1 {
		t.Fatalf("handler ran %d times", len(*events))
	}
}

// tsStatic lets the test pin the timestamp so signature input matches header.
var tsStatic = fmtI(time.Now().UnixMilli())

func mustTS() string { return tsStatic }

func fmtI(v int64) string { i := v; _ = i; return strings.TrimSpace(jsonNum(v)) }
func jsonNum(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	k := newStubKit(t)
	store, fn, _ := collect()
	h, _ := k.WebhookHandler(WebhookDeps{Store: store, Handle: fn})
	req := httptest.NewRequest(http.MethodPost, "/webhooks/waffo", strings.NewReader(`{"eventId":"X"}`))
	req.Header.Set("X-Waffo-Signature", "t=1,v1=deadbeef")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code %d", rec.Code)
	}
}

func TestWebhookHandlerErrorTriggersRetry(t *testing.T) {
	k := newStubKit(t)
	store := newMemStore()
	boom := true
	fn := func(context.Context, *Event) error {
		if boom {
			return context.DeadlineExceeded
		}
		return nil
	}
	h, _ := k.WebhookHandler(WebhookDeps{Store: store, Handle: fn})
	res, _ := k.CreateCheckout(context.Background(), CheckoutRequest{
		ProductID: "PROD_M", BuyerIdentity: "acct:r", ExternalID: "uc_sub:r",
	})
	sid := strings.TrimPrefix(res.CheckoutURL, "http://site.test/_waffo/stub/c?sid=")
	k.stub.mu.Lock()
	sess := k.stub.sess[sid]
	k.stub.mu.Unlock()
	body, _ := json.Marshal(k.stubEnvelope(EventSubscriptionActivated, sess.OrderID, sess))
	deliver := func() int {
		ts := fmtI(time.Now().UnixMilli())
		sig, _ := k.stub.sign(ts + "." + string(body))
		req := httptest.NewRequest(http.MethodPost, "/webhooks/waffo", strings.NewReader(string(body)))
		req.Header.Set("X-Waffo-Signature", "t="+ts+",v1="+sig)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := deliver(); code != http.StatusInternalServerError {
		t.Fatalf("handler error must 5xx, got %d", code)
	}
	if _, seen := store.seen[sess.OrderID]; seen {
		t.Fatal("failed event must not be marked seen")
	}
	boom = false
	if code := deliver(); code != http.StatusOK {
		t.Fatalf("retry must succeed, got %d", code)
	}
}

// --- live mode against a fake API host --------------------------------------

func TestLiveCheckoutAgainstFakeAPI(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pem := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	var mu sync.Mutex
	var seenPaths []string
	var lastSession map[string]any
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenPaths = append(seenPaths, r.URL.Path)
		mu.Unlock()
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/actions/auth/issue-session-token":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"token": "tok", "expiresAt": "later"}})
		case "/v1/actions/checkout/create-session":
			mu.Lock()
			lastSession = body
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				"sessionId": "SES_1", "checkoutUrl": "https://pay.test/store/slug/checkout/SES_1#token=tok", "expiresAt": "later"}})
		default:
			http.Error(w, "unexpected "+r.URL.Path, 404)
		}
	}))
	defer api.Close()

	t.Setenv("WAFFO_PRIVATE_KEY", pem)
	k, err := New(Settings{Mode: ModeLive, MerchantID: "MER_0aBcDeFgHiJkLmNoPqRsTu", Environment: "test", BaseURL: api.URL})
	if err != nil {
		t.Fatal(err)
	}
	res, err := k.CreateCheckout(context.Background(), CheckoutRequest{
		ProductID: "PROD_0aBcDeFgHiJkLmNoPqRsTu", BuyerIdentity: "acct:1", ExternalID: "uc_sub:1", BuyerEmail: "a@b.c", Currency: "CNY",
		Metadata: map[string]string{"plan": "month"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.CheckoutURL, "checkout/SES_1") {
		t.Fatalf("url %s", res.CheckoutURL)
	}
	mu.Lock()
	paths := strings.Join(seenPaths, ",")
	sess := lastSession
	mu.Unlock()
	for _, p := range []string{"/v1/actions/auth/issue-session-token", "/v1/actions/checkout/create-session"} {
		if !strings.Contains(paths, p) {
			t.Fatalf("missing %s in paths %s", p, paths)
		}
	}
	if sess["productId"] != "PROD_0aBcDeFgHiJkLmNoPqRsTu" || sess["orderMerchantExternalId"] != "uc_sub:1" {
		t.Fatalf("session body %+v", sess)
	}
	if sess["metadata"] == nil {
		t.Fatal("metadata dropped")
	}
}

func TestPortalLinkStub(t *testing.T) {
	k := newStubKit(t)
	u, err := k.PortalLink(context.Background(), "acct:x")
	if err != nil {
		t.Fatal(err)
	}
	if u != "http://site.test/billing?stub_portal=1" {
		t.Fatalf("portal %s", u)
	}
}

func TestCheckoutValidation(t *testing.T) {
	k := newStubKit(t)
	if _, err := k.CreateCheckout(context.Background(), CheckoutRequest{ProductID: "P"}); err == nil {
		t.Fatal("missing identity must fail")
	}
	if _, err := k.CreateCheckout(context.Background(), CheckoutRequest{
		BuyerIdentity: "acct:1", ExternalID: strings.Repeat("x", 200)}); err == nil {
		t.Fatal("overlong ext id must fail")
	}
}
