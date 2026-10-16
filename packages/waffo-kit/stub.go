package waffokit

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

// Stub is the local fake cashier. It issues checkouts, renders a minimal
// payment page ("pay / simulate renewal / simulate refund") and delivers
// real RSA-signed webhook payloads through the mounted WebhookHandler — so
// signature verification, dedupe and dispatch run exactly as in production.
type Stub struct {
	mu     sync.Mutex
	key    *rsa.PrivateKey
	pubPEM string
	sessN  int
	orders int
	sess   map[string]*stubSession
	relay  http.Handler // the host-mounted WebhookHandler
}

type stubSession struct {
	Sid       string
	Identity  string
	ExtID     string
	Product   string
	Currency  string
	Hint      StubHint
	OrderID   string
	PeriodEnd time.Time
	PeriodNo  int
}

func newStub() (*Stub, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	return &Stub{
		key:    key,
		pubPEM: strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))),
		sess:   map[string]*stubSession{},
	}, nil
}

func (s *Stub) publicKeyPEM() string { return s.pubPEM }

// setRelay wires the WebhookHandler so confirmations travel the full
// verify→dedupe→dispatch path. Called automatically by Kit.WebhookHandler.
func (s *Stub) setRelay(h http.Handler) { s.mu.Lock(); s.relay = h; s.mu.Unlock() }

// StubBaseURL overrides the public base used to build cashier links.
func (k *Kit) StubBaseURL() string { return strings.TrimRight(k.settings.PublicBase, "/") }

// createCheckout registers a fake session and returns the cashier link.
func (k *Kit) createStubCheckout(req CheckoutRequest, currency string) (*CheckoutResult, error) {
	st := k.stub
	st.mu.Lock()
	defer st.mu.Unlock()
	st.sessN++
	sid := fmt.Sprintf("SID_STUB_%06d", st.sessN)
	st.orders++
	period := req.StubHint.PeriodDays
	if period <= 0 {
		period = 30
	}
	sess := &stubSession{
		Sid: sid, Identity: req.BuyerIdentity, ExtID: req.ExternalID,
		Product: req.ProductID, Currency: currency, Hint: req.StubHint,
		OrderID:   fmt.Sprintf("ORD_STUB_%06d", st.orders),
		PeriodEnd: time.Now().UTC().Add(time.Duration(period) * 24 * time.Hour),
		PeriodNo:  1,
	}
	st.sess[sid] = sess
	base := k.StubBaseURL()
	if base == "" {
		return nil, fmt.Errorf("waffokit: stub mode needs WAFFO_PUBLIC_BASE")
	}
	return &CheckoutResult{
		CheckoutURL: base + "/_waffo/stub/c?sid=" + url.QueryEscape(sid),
		SessionID:   sid,
	}, nil
}

// StubHandler serves the fake cashier at the given mount path prefix
// (the path the host registered, e.g. "/_waffo/stub").
func (k *Kit) StubHandler(mount string) http.Handler {
	if !k.IsStub() {
		return http.NotFoundHandler()
	}
	mount = strings.TrimRight(mount, "/")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, mount)
		switch {
		case r.Method == http.MethodGet && (path == "/c" || path == "/"):
			k.stubCashierPage(w, r, mount)
		case r.Method == http.MethodPost && path == "/confirm":
			k.stubConfirm(w, r, mount)
		default:
			http.NotFound(w, r)
		}
	})
}

func (k *Kit) stubCashierPage(w http.ResponseWriter, r *http.Request, mount string) {
	sid := r.URL.Query().Get("sid")
	k.stub.mu.Lock()
	sess := k.stub.sess[sid]
	k.stub.mu.Unlock()
	if sess == nil {
		http.Error(w, "unknown stub session", http.StatusNotFound)
		return
	}
	plan, price := sess.Hint.PlanName, sess.Hint.Price
	if plan == "" {
		plan = sess.Product
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Waffo Stub 收银台</title>
<style>body{font-family:system-ui;background:#0b0b0d;color:#f5f5f7;display:grid;place-items:center;min-height:100vh;margin:0}
.card{background:#17171a;border:1px solid #2a2a2e;border-radius:16px;padding:32px;max-width:420px;width:92%%}
h1{font-size:20px;margin:0 0 4px}.muted{color:#98989d;font-size:13px}
.btn{display:block;width:100%%;margin-top:12px;padding:12px;border-radius:10px;border:1px solid #3a3a3f;background:#1f1f23;color:#f5f5f7;font-size:14px;cursor:pointer}
.btn.primary{background:#0a84ff;border-color:#0a84ff}
.badge{display:inline-block;background:#26262b;border-radius:6px;padding:2px 8px;font-size:12px;color:#98989d;margin-bottom:12px}</style></head>
<body><div class="card"><span class="badge">STUB · 本地假收银台</span>
<h1>%s</h1><p class="muted">身份 %s · 单号 %s<br>金额 %s · 周期 %d 天</p>
<form method="post" action="%s/confirm"><input type="hidden" name="sid" value="%s">
<button class="btn primary" name="action" value="pay">立即支付（发 subscription.activated）</button>
<button class="btn" name="action" value="renew">模拟续费（发 subscription.renewed）</button>
<button class="btn" name="action" value="refund">模拟退款（发 refund.succeeded）</button>
</form></div></body></html>`,
		html(plan), html(sess.Identity), html(sess.OrderID), html(price), daysOf(sess), mount, html(sid))
}

func (k *Kit) stubConfirm(w http.ResponseWriter, r *http.Request, mount string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	sid, action := r.FormValue("sid"), r.FormValue("action")
	k.stub.mu.Lock()
	sess := k.stub.sess[sid]
	if sess != nil {
		period := sess.Hint.PeriodDays
		if period <= 0 {
			period = 30
		}
		switch action {
		case "pay":
			// first payment: period end already set at session create
		case "renew":
			sess.PeriodNo++
			sess.PeriodEnd = sess.PeriodEnd.Add(time.Duration(period) * 24 * time.Hour)
		}
	}
	k.stub.mu.Unlock()
	if sess == nil {
		http.Error(w, "unknown stub session", http.StatusNotFound)
		return
	}
	evType, eventID := k.stubEventShape(action, sess)
	if evType == "" {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	body, err := json.Marshal(k.stubEnvelope(evType, eventID, sess))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := k.stubDeliver(body); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<body style="font-family:system-ui;background:#0b0b0d;color:#ff6961">stub deliver failed: %s</body>`, html(err.Error()))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><body style="font-family:system-ui;background:#0b0b0d;color:#f5f5f7;display:grid;place-items:center;min-height:100vh;margin:0"><div style="text-align:center"><h2 style="color:#30d158">支付成功（模拟）</h2><p style="color:#98989d">已投递签名事件 %s<br>可关闭窗口回站点查看订阅</p></div></body>`, html(evType))
}

func (k *Kit) stubEventShape(action string, s *stubSession) (string, string) {
	switch action {
	case "pay":
		return EventSubscriptionActivated, s.OrderID // eventId = Order ID
	case "renew":
		return EventSubscriptionRenewed, s.OrderID + "." + fmt.Sprint(s.PeriodEnd.Unix()) // OrderID + period end
	case "refund":
		return EventRefundSucceeded, "RFND_" + s.OrderID // Refund ID
	}
	return "", ""
}

func (k *Kit) stubEnvelope(evType, eventID string, s *stubSession) pancake.WebhookEvent {
	periodStart := s.PeriodEnd.Add(-24 * time.Hour * time.Duration(daysOf(s))).Format(time.RFC3339)
	end := s.PeriodEnd.Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)
	pn := s.PeriodNo
	data := pancake.WebhookEventData{
		OrderID: s.OrderID, BuyerEmail: "stub@local", Currency: s.Currency,
		Amount: "0", ProductName: or(s.Hint.PlanName, s.Product),
		OrderMerchantExternalID:       strPtr(s.ExtID),
		MerchantProvidedBuyerIdentity: strPtr(s.Identity),
		BillingPeriod:                 strPtr("monthly"),
		PeriodNumber:                  &pn,
		CurrentPeriodStart:            &periodStart, CurrentPeriodEnd: &end,
	}
	raw, _ := json.Marshal(data)
	return pancake.WebhookEvent{
		ID: "EVT_" + eventID, EventID: eventID, EventType: evType,
		Timestamp: now, Mode: pancake.EnvironmentTest,
		StoreID: "STO_STUB", StoreName: "local stub", Data: raw,
	}
}

// stubDeliver signs the payload with the stub key and POSTs it through the
// relayed WebhookHandler (full verify path) when mounted; otherwise errors.
func (k *Kit) stubDeliver(body []byte) error {
	k.stub.mu.Lock()
	relay := k.stub.relay
	k.stub.mu.Unlock()
	if relay == nil {
		return fmt.Errorf("waffokit: stub relay not mounted (call Kit.WebhookHandler first)")
	}
	ts := fmt.Sprint(time.Now().UnixMilli())
	sig, err := k.stub.sign(ts + "." + string(body))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "/_waffo/stub/relay", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Waffo-Signature", "t="+ts+",v1="+sig)
	rec := &captureWriter{hdr: http.Header{}}
	relay.ServeHTTP(rec, req)
	if rec.code >= 500 {
		return fmt.Errorf("waffokit: relay answered %d: %s", rec.code, rec.body.String())
	}
	return nil
}

func (s *Stub) sign(input string) (string, error) {
	h := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, h[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

type captureWriter struct {
	code int
	hdr  http.Header
	body strings.Builder
}

func (c *captureWriter) Header() http.Header { return c.hdr }
func (c *captureWriter) Write(b []byte) (int, error) {
	if c.code == 0 {
		c.code = http.StatusOK
	}
	return c.body.Write(b)
}
func (c *captureWriter) WriteHeader(code int) {
	if c.code == 0 {
		c.code = code
	}
}

func daysOf(s *stubSession) int {
	if s.Hint.PeriodDays > 0 {
		return s.Hint.PeriodDays
	}
	return 30
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func html(s string) string { return template.HTMLEscapeString(s) }
