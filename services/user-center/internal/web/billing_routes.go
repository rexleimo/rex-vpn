// billing_routes.go: 订阅购买闭环路由(W-C)。/billing 套餐页与收银台发起、
// /webhooks/waffo 公开回调(验签在 kit 内)、stub 假收银台仅本地模式挂载。
package web

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"vpnmaster/user-center/internal/billing"
	"vpnmaster/webkit"
)

// AttachBilling 注入订阅域(Router 前调用; nil = 订阅未启用, 走旧 bind/purchase 路径)。
func (s *Server) AttachBilling(bs *billing.Service) { s.billing = bs }

func (s *Server) mountBilling(e *gin.Engine) {
	if s.billing == nil {
		return
	}
	// 公开回调: Waffo 无会话, 验签+幂等在 kit.WebhookHandler 内完成。
	h, err := s.billing.WebhookHandler()
	if err != nil {
		s.logf("billing: webhook handler: %v", err)
		return
	}
	e.POST("/webhooks/waffo", gin.WrapH(h))
	if s.billing.IsStub() {
		// 本地假收银台(WAFFO_STUB=1): 签真 RSA 事件走同一验签链路。
		e.Any("/_waffo/stub/*rest", gin.WrapH(s.billing.Kit.StubHandler("/_waffo/stub")))
	}

	b := e.Group("/billing", s.billingGate())
	b.GET("", s.billingPage)
	b.POST("/checkout", s.billingCheckout)
	b.GET("/manage", s.billingManage)
}

// billingGate: 订阅域门槛。未登录→/login; legacy token 会话→/purchase(旧路径);
// 已登录账号(无论是否已有订阅)→放行。绝不能用通用 auth(): 未订阅用户会被
// 重定向到 /billing 自身形成循环。
func (s *Server) billingGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		v, ok := s.sessionValue(c.Request)
		if !ok {
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}
		if _, isAcct := acctID(v); !isAcct {
			c.Redirect(http.StatusSeeOther, "/purchase")
			c.Abort()
			return
		}
		c.Next()
	}
}

func (s *Server) billingAccount(c *gin.Context) string {
	v, _ := s.sessionValue(c.Request)
	id, _ := acctID(v)
	return id
}

type billingPlanView struct {
	Name, PriceStr, SpecStr string
}

type billingPageData struct {
	Plans  []billingPlanView
	Sub    *billingSubView
	Err    string
	Notice string
	Stub   bool
}

type billingSubView struct {
	PlanName  string
	Status    string
	PeriodEnd string
}

var subStatusCN = map[string]string{
	"active": "生效中", "canceling": "取消中(期末前可用)", "past_due": "续费扣款失败(平台重试中)",
	"canceled": "已到期", "refunded": "已退款停服", "unclaimed": "待关联",
}

func (s *Server) billingPage(c *gin.Context) {
	acct := s.billingAccount(c)
	d := billingPageData{Err: c.Query("err"), Notice: c.Query("ok"), Stub: s.billing.IsStub()}
	if c.Query("stub_portal") != "" {
		d.Notice = "stub 模式无托管门户; 生产环境此处跳 Waffo 客户门户自助管理"
	}
	for _, p := range s.billing.Plans() {
		d.Plans = append(d.Plans, billingPlanView{
			Name:     p.Name,
			PriceStr: fmt.Sprintf("¥%.2f", float64(p.Price)/100),
			SpecStr:  fmt.Sprintf("%s/月 · %d 天", humanBytes(p.Bytes), p.Days),
		})
	}
	if row, found, err := s.billing.Status(c.Request.Context(), acct); err == nil && found {
		st := row.Status
		if cn, ok := subStatusCN[st]; ok {
			st = cn
		}
		d.Sub = &billingSubView{PlanName: row.PlanName, Status: st,
			PeriodEnd: row.PeriodEnd.Format("2006-01-02")}
	}
	s.page(c, Page{Title: "订阅", Active: "purchase", Content: "pageBilling", PageData: d})
}

func (s *Server) billingCheckout(c *gin.Context) {
	jsonMode := webkit.WantsJSON(c)
	acct := s.billingAccount(c)
	email, _ := s.accounts.AccountEmail(c.Request.Context(), acct)
	plan := c.PostForm("plan")
	fail := func(msg string) {
		if jsonMode {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": msg})
			return
		}
		c.Redirect(http.StatusSeeOther, "/billing?err="+url.QueryEscape(msg))
	}
	loc, err := s.billing.StartCheckout(c.Request.Context(), acct, email, plan)
	if err != nil {
		s.logf("billing checkout %s: %v", acct, err)
		fail(err.Error())
		return
	}
	if jsonMode {
		c.JSON(http.StatusOK, gin.H{"ok": true, "redirect": loc})
		return
	}
	c.Redirect(http.StatusSeeOther, loc)
}

func (s *Server) billingManage(c *gin.Context) {
	acct := s.billingAccount(c)
	loc, err := s.billing.PortalURL(c.Request.Context(), acct)
	if err != nil {
		c.Redirect(http.StatusSeeOther, "/billing?err="+url.QueryEscape("门户暂不可用"))
		return
	}
	c.Redirect(http.StatusSeeOther, loc)
}
