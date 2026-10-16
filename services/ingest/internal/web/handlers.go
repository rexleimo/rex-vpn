package web

// handlers.go: 登录/登出 + SSR 页面 handler(总览/上游/节点/事件/用户)。
// 每页组装对应 *Data 视图模型后交 page() 渲染 layout。

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"vpnmaster/webui"
)

// ---- 登录/登出 ----

func (h *Handler) loginPage(c *gin.Context) {
	webui.WritePage(c.Writer, h.Template, "layout", Page{
		Title: "登录", CSS: webui.ThemeCSS(), Content: "login",
		PageData: map[string]any{"Err": c.Query("err") != ""},
	})
}

func (h *Handler) loginPost(c *gin.Context) {
	pw := c.PostForm("password")
	if !h.Session.LoginOK(pw) {
		c.Redirect(http.StatusSeeOther, "/login?err=1")
		return
	}
	h.Session.Issue(c.Writer)
	c.Redirect(http.StatusSeeOther, "/admin")
}

func (h *Handler) logout(c *gin.Context) {
	h.Session.Revoke(c.Writer, c.Request)
	c.Redirect(http.StatusSeeOther, "/login")
}

// ---- SSR 页 ----

func (h *Handler) page(c *gin.Context, p Page) {
	if h.Prober != nil {
		p.Degraded = h.Prober.Degraded()
	}
	// S4: app.css 旧 wm-* 令牌层已退役, 全站统一 Bootstrap + theme.css(ADR-0002)。
	p.CSS = webui.ThemeCSS()
	webui.WritePage(c.Writer, h.Template, "layout", p)
}

func (h *Handler) overview(c *gin.Context) {
	ctx := c.Request.Context()
	ups, _ := h.Store.ListUpstreams(ctx)
	nodes, _ := h.Store.ListNodes(ctx, "")
	evs, _ := h.Store.ListEvents(ctx, 12)
	counts := map[string]int{}
	for _, n := range nodes {
		counts[n.Status]++
	}
	uviews := make([]UpstreamView, 0, len(ups))
	for _, u := range ups {
		uc, _ := h.Store.CountNodesBySource(ctx, u.Name)
		uviews = append(uviews, h.upstreamView(u, uc))
	}
	h.page(c, Page{Title: "总览", Active: "overview", Content: "pageOverview",
		PageData: OverviewData{
			UpstreamCount: len(ups),
			NodeCounts:    counts,
			RecentEvents:  eventViews(evs),
			Upstreams:     uviews,
		}})
}

func (h *Handler) upstreams(c *gin.Context) {
	ctx := c.Request.Context()
	ups, _ := h.Store.ListUpstreams(ctx)
	views := make([]UpstreamView, 0, len(ups))
	for _, u := range ups {
		uc, _ := h.Store.CountNodesBySource(ctx, u.Name)
		views = append(views, h.upstreamView(u, uc))
	}
	h.page(c, Page{Title: "上游", Active: "upstreams", Content: "pageUpstreams",
		PageData: UpstreamsData{Upstreams: views, CanFetch: h.Poller != nil, Err: c.Query("err")}})
}

func (h *Handler) nodes(c *gin.Context) {
	nq := nodeQueryFrom(c)
	if c.Query("size") == "" {
		nq.Size = 25 // 页面默认 25/页; API 无参仍全量(旧脚本契约)
	}
	rows, total, _ := h.Store.QueryNodes(c.Request.Context(), nq)
	views := make([]NodeView, 0, len(rows))
	for _, n := range rows {
		views = append(views, h.nodeView(n))
	}
	h.page(c, Page{Title: "节点", Active: "nodes", Content: "pageNodes", PageData: NodesData{
		Nodes: views, Q: nq.Q, Status: nq.Status, Page: nq.Page, Size: nq.Size, Total: int(total),
	}})
}

func (h *Handler) events(c *gin.Context) {
	evs, _ := h.Store.ListEventsBefore(c.Request.Context(), 0, 51)
	hasMore := len(evs) > 50
	if hasMore {
		evs = evs[:50]
	}
	h.page(c, Page{Title: "事件", Active: "events", Content: "pageEvents",
		PageData: EventsData{Events: eventViews(evs), HasMore: hasMore}})
}

func (h *Handler) users(c *gin.Context) {
	var d UsersData
	if h.Marzban == nil {
		d = UsersData{ErrText: "MARZBAN_API_BASE 未配置, 用户页不可用 (work-6 联调后启用)"}
	} else if users, err := h.Marzban.ListUsers(c.Request.Context()); err != nil {
		d = UsersData{Configured: true, ErrText: err.Error(), Users: []UserView{}}
	} else {
		q := strings.ToLower(c.Query("q"))
		views := make([]UserView, 0, len(users))
		for _, u := range users {
			if q != "" && !strings.Contains(strings.ToLower(u.Username), q) && !strings.Contains(strings.ToLower(u.ID), q) {
				continue // S3: 服务端搜索(浏览器视角), Marzban API 无 q 参数时本地过滤
			}
			var exp string
			if u.ExpireTime > 0 {
				exp = time.Unix(u.ExpireTime, 0).Format("2006-01-02")
			} else {
				exp = "never"
			}
			variant := "danger"
			if u.Status == "active" {
				variant = "active"
			}
			views = append(views, UserView{
				ID: u.ID, Username: u.Username, Status: u.Status, StatusVar: variant,
				ExpireStr:  exp,
				TrafficStr: fmt.Sprintf("%d↑/%d↓", u.TotalUpload, u.TotalDownload),
			})
		}
		d = UsersData{Configured: true, Users: views, Q: c.Query("q")}
	}
	h.page(c, Page{Title: "用户", Active: "users", Content: "pageUsers", PageData: d})
}
