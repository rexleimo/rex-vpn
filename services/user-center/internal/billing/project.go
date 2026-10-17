// project.go: Waffo 事件 → Marzban 供给 + 订阅镜像的动作矩阵。
// 映射表来源: docs/plans/2026-10-17-waffo订阅闭环。所有动作 resync 事件事实, 幂等。
package billing

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"vpnmaster/user-center/internal/marzban"

	"github.com/rexleimo/waffo-kit"
)

// Projector 执行事件→供给动作。零值字段安全(缺依赖时对应动作跳过并记日志)。
type Projector struct {
	MZ     marzban.Client
	Links  Links
	Store  Store
	byName map[string]marzban.Plan
}

// Handle 是 waffokit 事件入口(已验签、已判重)。返回 error → 5xx → Waffo 重试。
func (p *Projector) Handle(ctx context.Context, ev *waffokit.Event) error {
	acct, _ := accountFrom(ev)
	if acct == "" {
		// 无法归属的事件 ack 掉并留痕: 重试也不会变出 identity, 5xx 只会无限重投。
		log.Printf("billing: event %s(%s) without buyer identity, acked unclaimed", ev.Type, ev.EventID)
		return nil
	}
	plan := p.planFor(ev)
	row := SubRow{AccountID: acct, OrderID: orderID(ev), PlanName: planName(ev, plan), Status: "active"}
	if t, ok := periodEnd(ev, plan); ok {
		row.PeriodEnd = t
	}

	switch ev.Type {
	case waffokit.EventSubscriptionActivated, waffokit.EventSubscriptionRenewed, waffokit.EventSubscriptionRecovered:
		return p.provisionOrExtend(ctx, acct, row, plan, true)

	case waffokit.EventSubscriptionPlanChanged:
		return p.provisionOrExtend(ctx, acct, row, plan, false)

	case waffokit.EventSubscriptionUncanceled:
		return p.provisionOrExtend(ctx, acct, row, plan, false)

	case waffokit.EventSubscriptionCanceling:
		row.Status = "canceling" // 期末前照常可用, 不动 Marzban
		return p.Store.UpsertSub(ctx, row)

	case waffokit.EventSubscriptionPastDue:
		row.Status = "past_due" // 平台自动重试; Marzban 按 expire 天然兜底
		return p.Store.UpsertSub(ctx, row)

	case waffokit.EventSubscriptionCanceled:
		row.Status = "canceled" // 不续期 = 自然到期
		return p.Store.UpsertSub(ctx, row)

	case waffokit.EventSubscriptionPlanChangeScheduled, waffokit.EventSubscriptionPlanChangeFailed:
		log.Printf("billing: %s for acct %s (%s): no immediate supply change", ev.Type, acct, row.PlanName)
		if cur, found, err := p.Store.GetSub(ctx, acct); err == nil && found {
			return p.Store.UpsertSub(ctx, cur) // no-op touch: 留 updated_at 痕迹
		}
		return nil

	case waffokit.EventSubscriptionPaymentSucceeded:
		// 纯支付事件(首购与 activated 双发): 记账, 不动周期。
		return nil

	case waffokit.EventRefundSucceeded:
		row.Status = "refunded"
		now := time.Now().UTC().Add(30 * time.Second) // 退款即断供(小额宽限防抖动)
		if err := p.modify(ctx, acct, marzban.UserMod{Expire: &[]int64{now.Unix()}[0]}); err != nil {
			log.Printf("billing: refund cutoff acct %s: %v", acct, err) // 用户已无供给也视为达成
		}
		return p.Store.UpsertSub(ctx, row)

	case waffokit.EventRefundFailed:
		log.Printf("billing: refund failed event %s acct %s — 需人工关注", ev.EventID, acct)
		return nil

	default:
		log.Printf("billing: unhandled event type %q", ev.Type)
		return nil
	}
}

// provisionOrExtend 有 link 且用户存在→延期(resync); 无 link→CreateUser 拿 token 并绑定。
// 409/NotFound 都收敛到"回读或重建", 保证重试与半成功安全。
func (p *Projector) provisionOrExtend(ctx context.Context, acct string, row SubRow, plan marzban.Plan, resetTraffic bool) error {
	expire := row.PeriodEnd.Unix()
	dataLimit := plan.Bytes
	username := SubUsername(acct)

	tok, bound, err := p.Links.LinkToken(ctx, acct)
	if err != nil {
		return fmt.Errorf("billing: link lookup: %w", err)
	}
	if !bound {
		cr, err := p.MZ.CreateUser(ctx, marzban.UserSpec{Username: username, Expire: expire, DataLimit: dataLimit})
		if err != nil {
			return fmt.Errorf("billing: provision %s: %w", username, err)
		}
		if err := p.Links.SetLink(ctx, acct, cr.Token); err != nil {
			return fmt.Errorf("billing: set link: %w", err)
		}
		return p.Store.UpsertSub(ctx, row)
	}
	_ = tok
	mod := marzban.UserMod{Expire: &expire}
	if dataLimit > 0 {
		mod.DataLimit = &dataLimit
	}
	if resetTraffic {
		zero := int64(0)
		mod.UsedTraffic = &zero
	}
	err = p.MZ.ModifyUser(ctx, username, mod)
	if err == marzban.ErrNotFound {
		// 面板侧用户被删过: 重建并换绑 token
		cr, cerr := p.MZ.CreateUser(ctx, marzban.UserSpec{Username: username, Expire: expire, DataLimit: dataLimit})
		if cerr != nil {
			return fmt.Errorf("billing: reprovision %s: %w", username, cerr)
		}
		if err := p.Links.SetLink(ctx, acct, cr.Token); err != nil {
			return fmt.Errorf("billing: relink: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("billing: extend %s: %w", username, err)
	}
	return p.Store.UpsertSub(ctx, row)
}

func (p *Projector) modify(ctx context.Context, acct string, mod marzban.UserMod) error {
	_, bound, err := p.Links.LinkToken(ctx, acct)
	if err != nil || !bound {
		return fmt.Errorf("billing: no link for acct")
	}
	return p.MZ.ModifyUser(ctx, SubUsername(acct), mod)
}

// planFor: 事件 ProductName → 本地套餐(配置约定: Waffo 产品名 = 套餐名)。
func (p *Projector) planFor(ev *waffokit.Event) marzban.Plan {
	if p.byName == nil || ev.Data == nil {
		return marzban.Plan{}
	}
	return p.byName[ev.Data.ProductName]
}

// SubUsername: 账号 ID → 稳定 Marzban 用户名(幂等键)。
func SubUsername(accountID string) string {
	var b []byte
	for _, r := range strings.ToLower(accountID) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b = append(b, byte(r))
		}
	}
	s := string(b)
	if len(s) > 16 {
		s = s[:16]
	}
	if s == "" {
		s = "acct"
	}
	return "uc_" + s // Marzban 用户名只允许 a-z 0-9 _(实测 422 验证)
}

func accountFrom(ev *waffokit.Event) (acct, identity string) {
	if ev.Data == nil {
		return "", ""
	}
	if ev.Data.MerchantProvidedBuyerIdentity != nil {
		identity = *ev.Data.MerchantProvidedBuyerIdentity
	}
	if identity != "" {
		if id, ok := strings.CutPrefix(identity, "acct:"); ok && id != "" {
			return id, identity
		}
		return identity, identity
	}
	if ev.Data.OrderMerchantExternalID != nil {
		// 兜底: uc_sub:<acct>
		if id, ok := strings.CutPrefix(*ev.Data.OrderMerchantExternalID, "uc_sub:"); ok && id != "" {
			return id, "acct:" + id
		}
	}
	return "", ""
}

func orderID(ev *waffokit.Event) string {
	if ev.Data == nil {
		return ""
	}
	return ev.Data.OrderID
}

func planName(ev *waffokit.Event, plan marzban.Plan) string {
	if plan.Name != "" {
		return plan.Name
	}
	if ev.Data != nil && ev.Data.ProductName != "" {
		return ev.Data.ProductName
	}
	return ""
}

// periodEnd: 事件 currentPeriodEnd(RFC3339)优先; 缺失时按套餐天数推算。
func periodEnd(ev *waffokit.Event, plan marzban.Plan) (time.Time, bool) {
	if ev.Data != nil && ev.Data.CurrentPeriodEnd != nil {
		if t, err := time.Parse(time.RFC3339, *ev.Data.CurrentPeriodEnd); err == nil {
			return t.UTC(), true
		}
	}
	if plan.Days > 0 {
		return time.Now().UTC().AddDate(0, 0, plan.Days), true
	}
	return time.Time{}, false
}
