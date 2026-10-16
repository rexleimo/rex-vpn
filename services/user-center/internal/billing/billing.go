// Package billing 把 Waffo 订阅事件投影为 Marzban 订阅供给(Waffo=计费真相源, 本包=幂等投影)。
// 原则: 事件携带的 currentPeriodEnd/identity 直接 resync, 不做增量算术;
// handler 失败返回 error → HTTP 5xx → Waffo 重试同一 eventId, Seen 表保证去重。
package billing

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"vpnmaster/user-center/internal/marzban"

	"github.com/rexleimo/waffo-kit"
)

// Links 是账号↔Marzban token 的服务端映射(accounts.Service 实现)。
type Links interface {
	LinkToken(ctx context.Context, accountID string) (string, bool, error)
	SetLink(ctx context.Context, accountID, token string) error
}

// SubRow 是账号订阅镜像行。
type SubRow struct {
	AccountID string
	OrderID   string
	PlanName  string
	Status    string // active|canceling|past_due|canceled|refunded|unclaimed
	PeriodEnd time.Time
	UpdatedAt time.Time
}

// Store 组合事件幂等表与订阅镜像(waffokit.EventStore 由 Seen/MarkSeen 满足)。
type Store interface {
	Seen(eventID string) (bool, error)
	MarkSeen(eventID, eventType string) error
	UpsertSub(ctx context.Context, row SubRow) error
	GetSub(ctx context.Context, accountID string) (SubRow, bool, error)
}

// Service 是 billing 域入口: 收银台发起 + webhook 接线。
type Service struct {
	Kit        *waffokit.Kit
	MZ         marzban.Client
	Links      Links
	Store      Store
	PublicBase string

	plans  []marzban.Plan
	byName map[string]marzban.Plan
}

// New 构造 Service。plans 用于套餐→Waffo 产品/流量/周期映射。
func New(kit *waffokit.Kit, mz marzban.Client, links Links, store Store, plans []marzban.Plan, publicBase string) *Service {
	by := map[string]marzban.Plan{}
	for _, p := range plans {
		by[p.Name] = p
	}
	return &Service{Kit: kit, MZ: mz, Links: links, Store: store, PublicBase: publicBase, plans: plans, byName: by}
}

// Plans 返回套餐目录(模板渲染用)。
func (s *Service) Plans() []marzban.Plan { return s.plans }

// Status 查账号订阅镜像。
func (s *Service) Status(ctx context.Context, accountID string) (SubRow, bool, error) {
	return s.Store.GetSub(ctx, accountID)
}

// StartCheckout 为账号创建订阅收银台。已有 active/canceling 镜像时拒绝(引导管理页)。
func (s *Service) StartCheckout(ctx context.Context, accountID, email, planName string) (string, error) {
	if accountID == "" {
		return "", fmt.Errorf("billing: account required")
	}
	plan, ok := s.byName[planName]
	if !ok {
		return "", fmt.Errorf("billing: unknown plan %q", planName)
	}
	product := plan.WaffoProduct
	if product == "" {
		if !s.Kit.IsStub() {
			return "", fmt.Errorf("billing: plan %q has no waffo product id (USERCENTER_PLANS 5th field)", plan.Name)
		}
		product = "PROD_STUB_" + stubSlug(plan.Name) // stub 不校验产品格式, 稳定可回测
	}
	if row, found, err := s.Store.GetSub(ctx, accountID); err == nil && found {
		switch row.Status {
		case "active", "canceling", "past_due":
			return "", fmt.Errorf("billing: 已有生效订阅(%s), 换套餐请走「管理订阅」", row.Status)
		}
	}
	ext := waffokit.ExternalIDFor("uc_sub", accountID)
	res, err := s.Kit.CreateCheckout(ctx, waffokit.CheckoutRequest{
		ProductID:     product,
		BuyerIdentity: "acct:" + accountID,
		ExternalID:    ext,
		BuyerEmail:    email,
		Currency:      plan.Currency,
		SuccessURL:    strings.TrimRight(s.PublicBase, "/") + "/dashboard?paid=1",
		StubHint:      waffokit.StubHint{PlanName: plan.Name, Price: fmt.Sprintf("%.2f", float64(plan.Price)/100), PeriodDays: plan.Days},
	})
	if err != nil {
		return "", err
	}
	return res.CheckoutURL, nil
}

// PortalURL 生成 Waffo 客户自助管理门户链接(按点击即时签发, 含 bearer 片段, 勿缓存)。
func (s *Service) PortalURL(ctx context.Context, accountID string) (string, error) {
	return s.Kit.PortalLink(ctx, "acct:"+accountID)
}

// IsStub 报告收银台是否为本地假模式。
func (s *Service) IsStub() bool { return s.Kit.IsStub() }

// WebhookHandler 组装验签+幂等+投影的 http.Handler(公开挂载, 无需会话)。
func (s *Service) WebhookHandler() (http.Handler, error) {
	pr := &Projector{MZ: s.MZ, Links: s.Links, Store: s.Store, byName: s.byName}
	return s.Kit.WebhookHandler(waffokit.WebhookDeps{Store: s.Store, Handle: pr.Handle})
}

func stubSlug(name string) string {
	var b []byte
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b = append(b, byte(r))
		} else if r > 'z' { // CJK 等: 每字符取模映射字母, 保证稳定且仅 [a-z]
			b = append(b, byte('a'+(r)%26))
		}
	}
	s := string(b)
	if s == "" {
		s = "plan"
	}
	return s
}
