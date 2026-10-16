package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"vpnmaster/ingest/internal/db"
	"vpnmaster/ingest/internal/out"
	"vpnmaster/ingest/internal/probe"
	"vpnmaster/ingest/internal/sub"
	"vpnmaster/webkit"
	"vpnmaster/webui"
)

// Handler 是 ingest 内嵌的 web 层(SSR 管理页 + JSON API, work-7)。
// 不做前后分离: 页面服务端渲染, vanilla JS 只做 5s 轮询刷新表格(7.3)。
// Web 层 = gin + webkit 共享套件(ADR-0001)。
type Handler struct {
	Store    *db.Store
	Poller   *sub.Poller // 可 nil(无来源时禁用手动拉取)
	Prober   *probe.Prober
	Syncer   *out.Syncer // 可 nil; 手动摘除/回池后触发一次 xraygen
	Marzban  *MarzbanClient // 可 nil
	Session  *Session
	Template *template.Template
}

// 各页数据视图(模板扁平化)。

type UpstreamView struct {
	Name        string
	URLMasked   string
	LastOKStr   string
	LastErr     string
	NodeCount   int
}

type NodeView struct {
	Fingerprint  string
	Addr         string
	Port         int
	Protocol     string
	Status       string
	StatusVar    string
	LatencyStr   string
	LastSeenStr  string
	Tag          string
}

type EventView struct {
	ID          int64 `json:"ID"`
	Kind        string
	KindVar     string
	NodeFP      string
	From        string
	To          string
	Detail      string
	AtStr       string
}

type UserView struct {
	ID         string
	Username   string
	Status     string
	ExpireStr  string
	TrafficStr string
}

type Page struct {
	Title    string
	Active   string
	Degraded bool
	CSS      template.CSS // 内联主题/app 样式为受信嵌入内容, 避开 <style> 上下文净化(ZgotmplZ)
	Content  string
	PageData any
}

type OverviewData struct {
	UpstreamCount int
	NodeCounts    map[string]int
	RecentEvents  []EventView
	Upstreams     []UpstreamView
}
type UpstreamsData struct {
	Upstreams []UpstreamView
	CanFetch  bool
	Err       string
}
type NodesData struct {
	Nodes  []NodeView
	Q      string
	Status string
	Page   int
	Size   int
	Total  int
}
type EventsData struct {
	Events  []EventView
	HasMore bool
}
type UsersData struct {
	Configured bool
	ErrText    string
	Users      []UserView
}

// NewHandler 构造并解析模板。
func NewHandler(store *db.Store, poller *sub.Poller, prober *probe.Prober, syncer *out.Syncer, mz *MarzbanClient, sess *Session) (*Handler, error) {
	t, err := webui.ComponentsTemplate(adminLayout, pageTemplates)
	if err != nil {
		return nil, err
	}
	return &Handler{Store: store, Poller: poller, Prober: prober, Syncer: syncer, Marzban: mz, Session: sess, Template: t}, nil
}

// triggerXraygen 手动摘除/回池后立即同步一次 xray-edge 配置(5.2 触发链)。
func triggerXraygen(ctx context.Context, h *Handler) error {
	if h.Syncer == nil {
		return nil
	}
	_, err := h.Syncer.Sync(ctx)
	return err
}

const adminCSP = "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self' data:; object-src 'none'; frame-ancestors 'self'"

// Router 挂载全部路由(gin.Engine 即 http.Handler)。/api/healthz 开放, 其余 /admin 与 /api 需会话。
func (h *Handler) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	e := gin.New()
	e.Use(gin.Recovery(), webkit.SecurityHeaders(adminCSP))

	e.GET("/login", h.loginPage)
	e.POST("/login", h.loginPost)
	e.GET("/logout", h.logout)
	e.GET("/api/healthz", h.apiHealthz)
	e.GET("/", func(c *gin.Context) { c.Redirect(http.StatusSeeOther, "/admin") })

	// vendored 静态资产(零构建, 无 CDN)
	e.GET("/assets/bootstrap.min.css", gin.WrapH(webui.AssetHandler("bootstrap.min.css")))
	e.GET("/assets/bootstrap.bundle.min.js", gin.WrapH(webui.AssetHandler("bootstrap.bundle.min.js")))
	e.GET("/assets/wm-ui.js", gin.WrapH(webui.AssetHandler("wm-ui.js")))

	ad := e.Group("/admin", h.guard())
	ad.GET("", h.overview)
	ad.GET("/upstreams", h.upstreams)
	ad.POST("/upstreams", h.upstreamAdd)
	ad.POST("/upstreams/update", h.upstreamUpdate)
	ad.POST("/upstreams/delete", h.upstreamDelete)
	ad.POST("/upstreams/fetch", h.upstreamFetch)
	ad.GET("/nodes", h.nodes)
	ad.POST("/nodes/evict", h.nodeEvict)
	ad.POST("/nodes/recover", h.nodeRecover)
	ad.GET("/events", h.events)
	ad.GET("/users", h.users)

	api := e.Group("/api", h.guardAPI())
	api.GET("/upstreams", h.apiUpstreams)
	api.GET("/nodes", h.apiNodes)
	api.GET("/events", h.apiEvents)
	api.GET("/users", h.apiUsers)

	return e
}

// guard HTML 页会话门槛(未登录 303 → /login)。
func (h *Handler) guard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !h.Session.LoggedIn(c.Request) {
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}
		c.Next()
	}
}

// guardAPI JSON 会话门槛(未登录 401)。
func (h *Handler) guardAPI() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !h.Session.LoggedIn(c.Request) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

// ---- 登录/登出 ----

func (h *Handler) loginPage(c *gin.Context) {
	webui.WritePage(c.Writer, h.Template, "layout", Page{
		Title: "登录", CSS: template.CSS(string(webui.CSS()) + string(webui.ThemeCSS())), Content: "login",
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
	// 过渡期双样式层: app.css(未迁移页) + theme.css(Bootstrap 主题), ADR-0002 后果。
	p.CSS = template.CSS(webui.CSS() + string(webui.ThemeCSS()))
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

// nodeQueryFrom 解析 ?q=&status=&page=&size=; 默认 size=25(页面), API 无 size=全量兼容旧脚本。
func nodeQueryFrom(c *gin.Context) db.NodeQuery {
	nq := db.NodeQuery{Q: c.Query("q"), Status: c.Query("status"), Page: atoiOr(c.Query("page"), 1)}
	if v := c.Query("size"); v != "" {
		nq.Size = atoiOr(v, 0)
	}
	return nq
}

func atoiOr(v string, def int) int {
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return n
	}
	return def
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
		views := make([]UserView, 0, len(users))
		for _, u := range users {
			var exp string
			if u.ExpireTime > 0 {
				exp = time.Unix(u.ExpireTime, 0).Format("2006-01-02")
			} else {
				exp = "never"
			}
			views = append(views, UserView{
				ID: u.ID, Username: u.Username, Status: u.Status,
				ExpireStr: exp,
				TrafficStr: fmt.Sprintf("%d↑/%d↓", u.TotalUpload, u.TotalDownload),
			})
		}
		d = UsersData{Configured: true, Users: views}
	}
	h.page(c, Page{Title: "用户", Active: "users", Content: "pageUsers", PageData: d})
}

// ---- 管理操作 (7.2 + S1 CRUD 闭环/ADR-0004) ----
// 双模式: wm-ui.js fetch(X-WM-Fetch: json) → JSON {ok,flash|error}; 无 JS 表单 → 303 + ?err= 兜底。

func replyOK(c *gin.Context, redirect, flash string) {
	if webkit.WantsJSON(c) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "flash": flash})
		return
	}
	c.Redirect(http.StatusSeeOther, redirect)
}

func replyErr(c *gin.Context, redirect string, code int, msg string) {
	if webkit.WantsJSON(c) {
		c.JSON(code, gin.H{"ok": false, "error": msg})
		return
	}
	c.Redirect(http.StatusSeeOther, redirect+"?err="+url.QueryEscape(msg))
}

const upsRedirect = "/admin/upstreams"

func (h *Handler) upstreamAdd(c *gin.Context) {
	name := c.PostForm("name")
	urlStr := c.PostForm("url")
	if name == "" || urlStr == "" {
		replyErr(c, upsRedirect, http.StatusBadRequest, "名称与 URL 必填")
		return
	}
	created, err := h.Store.AddUpstream(c.Request.Context(), name, urlStr)
	if err != nil {
		replyErr(c, upsRedirect, http.StatusInternalServerError, "入库失败: "+err.Error())
		return
	}
	if !created {
		replyErr(c, upsRedirect, http.StatusConflict, "来源 "+name+" 已存在")
		return
	}
	if h.Poller != nil {
		h.Poller.AddSource(sub.Source{Name: name, URL: urlStr})
		_ = h.Poller.PollOnceFor(c.Request.Context(), name)
	}
	replyOK(c, upsRedirect, "已添加上游 "+name)
}

// upstreamUpdate 编辑上游 URL(S1: 编辑不再删后重加)。name 是 nodes.source 外键不可改。
func (h *Handler) upstreamUpdate(c *gin.Context) {
	name := c.PostForm("name")
	urlStr := c.PostForm("url")
	if name == "" || urlStr == "" {
		replyErr(c, upsRedirect, http.StatusBadRequest, "名称与 URL 必填")
		return
	}
	updated, err := h.Store.UpdateUpstreamURL(c.Request.Context(), name, urlStr)
	if err != nil {
		replyErr(c, upsRedirect, http.StatusInternalServerError, "更新失败: "+err.Error())
		return
	}
	if !updated {
		replyErr(c, upsRedirect, http.StatusNotFound, "来源 "+name+" 不存在")
		return
	}
	if h.Poller != nil && !h.Poller.UpdateSource(name, urlStr) {
		// 入库已改但运行期拉取清单无此源(env 外手动添加的源重启后丢失, 既有语义)
		replyOK(c, upsRedirect, "已更新展示, 但该来源不在拉取清单(需重新添加才能拉取)")
		return
	}
	replyOK(c, upsRedirect, "已更新 "+name)
}

func (h *Handler) upstreamDelete(c *gin.Context) {
	name := c.PostForm("name")
	if err := h.Store.DeleteUpstream(c.Request.Context(), name); err != nil {
		replyErr(c, upsRedirect, http.StatusInternalServerError, "删除失败: "+err.Error())
		return
	}
	if h.Poller != nil {
		h.Poller.RemoveSource(name) // 不同步摘除会把已删行复活(pollOne 幂等登记)
	}
	replyOK(c, upsRedirect, "已删除 "+name)
}

func (h *Handler) upstreamFetch(c *gin.Context) {
	if h.Poller == nil {
		replyErr(c, upsRedirect, http.StatusServiceUnavailable, "无已配置上游")
		return
	}
	name := c.PostForm("name")
	if name != "" {
		if errs := h.Poller.PollOnceFor(c.Request.Context(), name); len(errs) > 0 {
			replyErr(c, upsRedirect, http.StatusBadGateway, fmt.Sprint(errs[0]))
			return
		}
		replyOK(c, upsRedirect, "已拉取 "+name)
		return
	}
	_ = h.Poller.PollOnce(c.Request.Context())
	replyOK(c, upsRedirect, "拉取已触发")
}

func (h *Handler) nodeEvict(c *gin.Context) {
	fp := c.PostForm("fp")
	if _, err := h.Store.Evict(c.Request.Context(), fp); err != nil {
		replyErr(c, "/admin/nodes", http.StatusInternalServerError, "摘除失败: "+err.Error())
		return
	}
	if err := triggerXraygen(c.Request.Context(), h); err != nil {
		replyErr(c, "/admin/nodes", http.StatusBadGateway, "已摘除但 xray 同步失败: "+err.Error())
		return
	}
	replyOK(c, "/admin/nodes", "已摘除并同步出口")
}

func (h *Handler) nodeRecover(c *gin.Context) {
	fp := c.PostForm("fp")
	if _, err := h.Store.Recover(c.Request.Context(), fp); err != nil {
		replyErr(c, "/admin/nodes", http.StatusInternalServerError, "回池失败: "+err.Error())
		return
	}
	if err := triggerXraygen(c.Request.Context(), h); err != nil {
		replyErr(c, "/admin/nodes", http.StatusBadGateway, "已回池但 xray 同步失败: "+err.Error())
		return
	}
	replyOK(c, "/admin/nodes", "已回池并同步出口")
}

// ---- JSON API (7.4): 供轮询(7.3)/未来客户端/监控 ----

func (h *Handler) apiHealthz(c *gin.Context) {
	degraded := false
	if h.Prober != nil {
		degraded = h.Prober.Degraded()
	}
	status := http.StatusOK
	if degraded {
		status = http.StatusServiceUnavailable
	}
	writeJSON(c.Writer, status, map[string]any{"status": "ok", "degraded": degraded})
}

func (h *Handler) apiUpstreams(c *gin.Context) {
	ups, _ := h.Store.ListUpstreams(c.Request.Context())
	out := make([]map[string]any, 0, len(ups))
	for _, u := range ups {
		uc, _ := h.Store.CountNodesBySource(c.Request.Context(), u.Name)
		out = append(out, map[string]any{
			"name": u.Name, "url_masked": u.URLMasked,
			"last_ok_at": u.LastOKAt.Format(time.RFC3339), "last_err": u.LastErr,
			"node_count": uc,
		})
	}
	writeJSON(c.Writer, http.StatusOK, out)
}

func (h *Handler) apiNodes(c *gin.Context) {
	nq := nodeQueryFrom(c)
	rows, total, err := h.Store.QueryNodes(c.Request.Context(), nq)
	if err != nil {
		writeJSON(c.Writer, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	views := make([]NodeView, 0, len(rows))
	for _, n := range rows {
		views = append(views, h.nodeView(n))
	}
	// 数组体保持旧契约(chaos/load-50); 总数走 header, 分页方读 X-Total-Count。
	c.Header("X-Total-Count", strconv.FormatInt(total, 10))
	writeJSON(c.Writer, http.StatusOK, views)
}

func (h *Handler) apiEvents(c *gin.Context) {
	before := int64(atoiOr(c.Query("before"), 0))
	limit := atoiOr(c.Query("limit"), 200) // 无参=200 与旧契约一致(chaos 断言窗口)
	evs, err := h.Store.ListEventsBefore(c.Request.Context(), before, limit)
	if err != nil {
		writeJSON(c.Writer, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(c.Writer, http.StatusOK, eventViews(evs)) // EventView.ID = 游标: 下一页 before=末行 id
}

func (h *Handler) apiUsers(c *gin.Context) {
	if h.Marzban == nil {
		writeJSON(c.Writer, http.StatusServiceUnavailable, map[string]any{"error": "marzban not configured"})
		return
	}
	users, err := h.Marzban.ListUsers(c.Request.Context())
	if err != nil {
		writeJSON(c.Writer, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(c.Writer, http.StatusOK, users)
}

// ---- 视图辅助 ----

func (h *Handler) upstreamView(u db.Upstream, nodeCount int64) UpstreamView {
	return UpstreamView{
		Name: u.Name, URLMasked: u.URLMasked,
		LastOKStr: fmtTime(u.LastOKAt), LastErr: u.LastErr, NodeCount: int(nodeCount),
	}
}

func (h *Handler) nodeView(n db.NodeRow) NodeView {
	variant := "pending"
	switch n.Status {
	case "active":
		variant = "active"
	case "evicted":
		variant = "evicted"
	}
	lat := "—"
	if n.LatencyMS > 0 {
		lat = fmt.Sprintf("%dms", n.LatencyMS)
	}
	return NodeView{
		Fingerprint: n.Fingerprint, Addr: n.Address, Port: n.Port,
		Protocol: n.Protocol, Status: n.Status, StatusVar: variant,
		LatencyStr: lat, LastSeenStr: fmtTime(n.LastSeenAt), Tag: n.Tag,
	}
}

func eventViews(evs []db.Event) []EventView {
	out := make([]EventView, 0, len(evs))
	for _, e := range evs {
		out = append(out, EventView{
			ID: e.ID, Kind: e.Kind, KindVar: eventVariant(e.Kind),
			NodeFP: e.NodeFP, From: e.From, To: e.To, Detail: e.Detail,
			AtStr: fmtTime(e.At),
		})
	}
	return out
}

func eventVariant(kind string) string {
	switch kind {
	case "evict", "pull_err", "stale":
		return "danger"
	case "recover", "config_write":
		return "success"
	default:
		return "info"
	}
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("2006-01-02 15:04:05")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}
