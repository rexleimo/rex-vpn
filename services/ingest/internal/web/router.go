package web

// router.go: Handler 构造 + gin 路由装配 + 会话门槛 + 视图模型/共享辅助。
// 页面/操作/API 三类 handler 各在 handlers.go / actions.go / api.go(单一职责)。

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
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
	Syncer   *out.Syncer    // 可 nil; 手动摘除/回池后触发一次 xraygen
	Marzban  *MarzbanClient // 可 nil
	Session  *Session
	Template *template.Template
}

// ---- 视图模型(模板扁平化 + JSON API 复用) ----

type UpstreamView struct {
	Name      string
	URLMasked string
	LastOKStr string
	LastErr   string
	NodeCount int
	Tracking  bool // 在 poller 运行清单内(DB 有行但不在清单 = 重启丢失/旧死记录, 不会被拉)
}

type NodeView struct {
	Fingerprint string
	Addr        string
	Port        int
	Protocol    string
	Status      string
	StatusVar   string
	LatencyStr  string
	LastSeenStr string
	Tag         string
}

type EventView struct {
	ID      int64 `json:"ID"`
	Kind    string
	KindVar string
	NodeFP  string
	From    string
	To      string
	Detail  string
	AtStr   string
}

type UserView struct {
	ID         string
	Username   string
	Status     string
	StatusVar  string
	ExpireStr  string
	TrafficStr string
}

type Page struct {
	Title    string
	Active   string
	Degraded bool
	CSS      template.CSS // 内联主题层为受信嵌入内容, 避开 <style> 上下文净化(ZgotmplZ)
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
	Q          string
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

// ---- 共享辅助: 查询解析 + 视图映射 + 时间/JSON ----

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

func (h *Handler) upstreamView(u db.Upstream, nodeCount int64) UpstreamView {
	return UpstreamView{
		Name: u.Name, URLMasked: u.URLMasked,
		LastOKStr: fmtTime(u.LastOKAt), LastErr: u.LastErr, NodeCount: int(nodeCount),
		Tracking: h.Poller != nil && h.Poller.HasSource(u.Name),
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
