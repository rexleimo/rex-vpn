// 事件→动作矩阵单测: fake links + marzban.Mock + MemStore(不依赖 PG/网络)。
package billing

import (
	"context"
	"strings"
	"testing"
	"time"

	"vpnmaster/user-center/internal/marzban"

	"github.com/rexleimo/waffo-kit"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

type fakeLinks struct {
	m   map[string]string
	set []string
}

func newFakeLinks() *fakeLinks { return &fakeLinks{m: map[string]string{}} }

func (f *fakeLinks) LinkToken(ctx context.Context, acct string) (string, bool, error) {
	t, ok := f.m[acct]
	return t, ok, nil
}
func (f *fakeLinks) SetLink(ctx context.Context, acct, tok string) error {
	f.m[acct] = tok
	f.set = append(f.set, acct)
	return nil
}

var plans = []marzban.Plan{
	{Name: "月付基础", Price: 2900, Bytes: 50 << 30, Days: 30, WaffoProduct: "PROD_month"},
	{Name: "月付高级", Price: 5900, Bytes: 200 << 30, Days: 30, WaffoProduct: "PROD_pro"},
}

func projector() (*Projector, *fakeLinks, *marzban.Mock, *MemStore) {
	links := newFakeLinks()
	mz := marzban.NewMock()
	st := NewMemStore()
	p := &Projector{MZ: mz, Links: links, Store: st, byName: map[string]marzban.Plan{}}
	for _, pl := range plans {
		p.byName[pl.Name] = pl
	}
	return p, links, mz, st
}

func ev(typ, identity, product, periodEnd string) *waffokit.Event {
	ext := waffokit.ExternalIDFor("uc_sub", identity)
	return &waffokit.Event{ID: "E1", EventID: typ + "-EID-" + identity, Type: typ,
		Data: waffoData(identity, ext, product, periodEnd)}
}

func waffoData(identity, ext, product, periodEnd string) *pancake.WebhookEventData {
	i, x, pe := identity, ext, periodEnd
	return &pancake.WebhookEventData{
		OrderID:                       "ORD_1",
		ProductName:                   product,
		MerchantProvidedBuyerIdentity: &i,
		OrderMerchantExternalID:       &x,
		CurrentPeriodEnd:              &pe,
	}
}

func TestActivatedProvisions(t *testing.T) {
	p, links, mz, st := projector()
	end := time.Now().UTC().Add(30 * 24 * time.Hour).Format(time.RFC3339)
	e := ev(waffokit.EventSubscriptionActivated, "abc123456", "月付基础", end)
	if err := p.Handle(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(mz.Created) != 1 || mz.Created[0].Username != SubUsername("abc123456") {
		t.Fatalf("created %+v", mz.Created)
	}
	if links.m["abc123456"] == "" {
		t.Fatal("link not set")
	}
	row, ok, _ := st.GetSub(context.Background(), "abc123456")
	if !ok || row.Status != "active" || row.PlanName != "月付基础" {
		t.Fatalf("mirror %+v", row)
	}
	if row.PeriodEnd.IsZero() {
		t.Fatal("period end missing")
	}
}

func TestRenewedExtendsAndResets(t *testing.T) {
	ctx := context.Background()
	p, _, mz, st := projector()
	e1 := ev(waffokit.EventSubscriptionActivated, "acctid1", "月付高级", iso(nowPlus(30)))
	if err := p.Handle(ctx, e1); err != nil {
		t.Fatal(err)
	}
	e2 := ev(waffokit.EventSubscriptionRenewed, "acctid1", "月付高级", iso(nowPlus(60)))
	e2.EventID = "renew-2"
	if err := p.Handle(ctx, e2); err != nil {
		t.Fatal(err)
	}
	mod := mz.Mods[SubUsername("acctid1")]
	if mod.Expire == nil || *mod.Expire != mustUnix(iso(nowPlus(60))) {
		t.Fatalf("expire not resynced: %+v", mod)
	}
	if mod.UsedTraffic == nil || *mod.UsedTraffic != 0 {
		t.Fatalf("traffic not reset: %+v", mod)
	}
	_ = st
	if mod.DataLimit == nil || *mod.DataLimit != 200<<30 {
		t.Fatalf("limit not applied: %+v", mod)
	}
}

func TestModifyNotFoundReprovisions(t *testing.T) {
	ctx := context.Background()
	p, links, _, _ := projector()
	e := ev(waffokit.EventSubscriptionActivated, "ghost1", "月付基础", iso(nowPlus(30)))
	if err := p.Handle(ctx, e); err != nil {
		t.Fatal(err)
	}
	// 模拟面板侧删号: 直接换 mock 使 Modify 找不到, 但 link 还在
	p.MZ = marzban.NewMock() // 新 mock, 无该用户, 但记录 Created
	links.m["ghost1"] = "tok-x"
	e2 := ev(waffokit.EventSubscriptionRenewed, "ghost1", "月付基础", iso(nowPlus(60)))
	e2.EventID = "r2"
	if err := p.Handle(ctx, e2); err != nil {
		t.Fatal(err)
	}
	mz2 := p.MZ.(*marzban.Mock)
	if len(mz2.Created) != 1 || mz2.Created[0].Username != SubUsername("ghost1") {
		t.Fatalf("expected reprovision, created=%+v", mz2.Created)
	}
	if links.m["ghost1"] != mz2.Created[0].Token {
		t.Fatalf("relink missing: %s", links.m["ghost1"])
	}
}

func TestLifecycleStatusMirrors(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		typ, want string
	}{
		{waffokit.EventSubscriptionCanceling, "canceling"},
		{waffokit.EventSubscriptionPastDue, "past_due"},
		{waffokit.EventSubscriptionCanceled, "canceled"},
	}
	for _, tc := range cases {
		p, _, _, st := projector()
		if err := p.Handle(ctx, ev(waffokit.EventSubscriptionActivated, "s1", "月付基础", iso(nowPlus(30)))); err != nil {
			t.Fatal(err)
		}
		e := ev(tc.typ, "s1", "月付基础", iso(nowPlus(30)))
		e.EventID = "x" + tc.typ
		if err := p.Handle(ctx, e); err != nil {
			t.Fatalf("%s: %v", tc.typ, err)
		}
		row, _, _ := st.GetSub(ctx, "s1")
		if row.Status != tc.want {
			t.Fatalf("%s → %s want %s", tc.typ, row.Status, tc.want)
		}
	}
}

func TestPaymentSucceededNoAction(t *testing.T) {
	ctx := context.Background()
	p, _, mz, st := projector()
	before := len(mz.Created)
	e := ev(waffokit.EventSubscriptionPaymentSucceeded, "pp1", "月付基础", iso(nowPlus(30)))
	if err := p.Handle(ctx, e); err != nil {
		t.Fatal(err)
	}
	if len(mz.Created) != before {
		t.Fatal("payment_succeeded must not provision")
	}
	if _, ok, _ := st.GetSub(ctx, "pp1"); ok {
		t.Fatal("payment_succeeded must not touch mirror")
	}
}

func TestRefundCutoff(t *testing.T) {
	ctx := context.Background()
	p, _, mz, st := projector()
	if err := p.Handle(ctx, ev(waffokit.EventSubscriptionActivated, "rf1", "月付基础", iso(nowPlus(30)))); err != nil {
		t.Fatal(err)
	}
	e := ev(waffokit.EventRefundSucceeded, "rf1", "月付基础", iso(nowPlus(30)))
	e.EventID = "rf-eid"
	if err := p.Handle(ctx, e); err != nil {
		t.Fatal(err)
	}
	mod := mz.Mods[SubUsername("rf1")]
	if mod.Expire == nil || *mod.Expire < time.Now().Unix()-10 {
		t.Fatalf("cutoff expire not now: %+v", mod)
	}
	row, _, _ := st.GetSub(ctx, "rf1")
	if row.Status != "refunded" {
		t.Fatalf("status %s", row.Status)
	}
}

func TestPlanChangedAppliesNewLimit(t *testing.T) {
	ctx := context.Background()
	p, _, mz, st := projector()
	if err := p.Handle(ctx, ev(waffokit.EventSubscriptionActivated, "pc1", "月付基础", iso(nowPlus(30)))); err != nil {
		t.Fatal(err)
	}
	e := ev(waffokit.EventSubscriptionPlanChanged, "pc1", "月付高级", iso(nowPlus(45)))
	e.EventID = "pc-e"
	if err := p.Handle(ctx, e); err != nil {
		t.Fatal(err)
	}
	mod := mz.Mods[SubUsername("pc1")]
	if mod.DataLimit == nil || *mod.DataLimit != 200<<30 {
		t.Fatalf("new limit not applied: %+v", mod)
	}
	row, _, _ := st.GetSub(ctx, "pc1")
	if row.PlanName != "月付高级" {
		t.Fatalf("plan name %s", row.PlanName)
	}
}

func TestNoIdentityAckedNoSideEffects(t *testing.T) {
	ctx := context.Background()
	p, _, mz, st := projector()
	e := &waffokit.Event{Type: waffokit.EventSubscriptionActivated, EventID: "noid", Data: &pancake.WebhookEventData{OrderID: "O1"}}
	if err := p.Handle(ctx, e); err != nil {
		t.Fatalf("must ack: %v", err)
	}
	if len(mz.Created) != 0 {
		t.Fatal("no provisioning without identity")
	}
	if len(st.subs) != 0 {
		t.Fatal("no mirror without identity")
	}
}

func TestExtIDFallback(t *testing.T) {
	ctx := context.Background()
	p, links, _, st := projector()
	ext := waffokit.ExternalIDFor("uc_sub", "fallback1")
	pe := iso(nowPlus(30))
	pr := "月付基础"
	e := &waffokit.Event{Type: waffokit.EventSubscriptionActivated, EventID: "fb",
		Data: &pancake.WebhookEventData{OrderID: "O2", ProductName: pr, OrderMerchantExternalID: &ext, CurrentPeriodEnd: &pe}}
	if err := p.Handle(ctx, e); err != nil {
		t.Fatal(err)
	}
	if _, ok := links.m["fallback1"]; !ok {
		t.Fatal("ext id fallback failed")
	}
	row, _, _ := st.GetSub(ctx, "fallback1")
	if !strings.Contains(row.OrderID, "O2") {
		t.Fatalf("order id %+v", row)
	}
}

func TestSubUsernameStability(t *testing.T) {
	if SubUsername("c85ef835-de62-46f2") != SubUsername("c85ef835-de62-46f2") {
		t.Fatal("not deterministic")
	}
	u := SubUsername("ABC-def_123")
	if u != "uc_abcdef123" {
		t.Fatalf("got %q", u)
	}
	if len(SubUsername(strings.Repeat("a", 100))) > 20 {
		t.Fatal("username too long")
	}
}

func iso(t time.Time) string { return t.UTC().Format(time.RFC3339) }
func nowPlus(days int) time.Time {
	return time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour)
}
func mustUnix(s string) int64 {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.Unix()
}
