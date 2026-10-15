package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"vpnmaster/ingest/internal/db"
	"vpnmaster/ingest/internal/out"
	"vpnmaster/ingest/internal/probe"
	"vpnmaster/ingest/internal/sub"
	"vpnmaster/webui"
)
// Handler 是 ingest 内嵌的 web 层(SSR 管理页 + JSON API, work-7)。
// 不做前后分离: 页面服务端渲染, vanilla JS 只做 5s 轮询刷新表格(7.3)。
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
	CSS      string
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
	Nodes []NodeView
}
type EventsData struct {
	Events []EventView
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

// Router 挂载全部路由。/api/healthz 开放, 其余 /admin 与 /api 需会话。
func (h *Handler) Router() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /login", h.loginPage)
	mux.HandleFunc("POST /login", h.loginPost)
	mux.HandleFunc("GET /logout", h.logout)

	mux.HandleFunc("GET /admin", h.guard(h.overview))
	mux.HandleFunc("GET /admin/upstreams", h.guard(h.upstreams))
	mux.HandleFunc("POST /admin/upstreams", h.guard(h.upstreamAdd))
	mux.HandleFunc("POST /admin/upstreams/delete", h.guard(h.upstreamDelete))
	mux.HandleFunc("POST /admin/upstreams/fetch", h.guard(h.upstreamFetch))
	mux.HandleFunc("GET /admin/nodes", h.guard(h.nodes))
	mux.HandleFunc("POST /admin/nodes/evict", h.guard(h.nodeEvict))
	mux.HandleFunc("POST /admin/nodes/recover", h.guard(h.nodeRecover))
	mux.HandleFunc("GET /admin/events", h.guard(h.events))
	mux.HandleFunc("GET /admin/users", h.guard(h.users))

	mux.HandleFunc("GET /api/healthz", h.apiHealthz)
	mux.HandleFunc("GET /api/upstreams", h.guardAPI(h.apiUpstreams))
	mux.HandleFunc("GET /api/nodes", h.guardAPI(h.apiNodes))
	mux.HandleFunc("GET /api/events", h.guardAPI(h.apiEvents))
	mux.HandleFunc("GET /api/users", h.guardAPI(h.apiUsers))

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	})
	return mux
}

// guard HTML 页会话门槛(未登录 302 → /login)。
func (h *Handler) guard(f func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.Session.Guard(w, r, func(w http.ResponseWriter, r *http.Request) {
			f(w, r)
		})
	}
}

// guardAPI JSON 会话门槛(未登录 401)。
func (h *Handler) guardAPI(f func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := ""
		if c, err := r.Cookie(cookieName); err == nil {
			tok = c.Value
		}
		if !h.Session.Valid(tok) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		f(w, r)
	}
}

// ---- 登录/登出 ----

func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	webui.WritePage(w, h.Template, "layout", Page{
		Title: "登录", CSS: webui.CSS(), Content: "login",
		PageData: map[string]any{"Err": r.URL.Query().Get("err") != ""},
	})
}

func (h *Handler) loginPost(w http.ResponseWriter, r *http.Request) {
	pw := r.FormValue("password")
	if !h.Session.LoginOK(pw) {
		http.Redirect(w, r, "/login?err=1", http.StatusSeeOther)
		return
	}
	h.Session.Issue(w)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	tok := ""
	if c, err := r.Cookie(cookieName); err == nil {
		tok = c.Value
	}
	h.Session.Revoke(w, tok)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---- SSR 页 ----

func (h *Handler) page(w http.ResponseWriter, p Page) {
	if h.Prober != nil {
		p.Degraded = h.Prober.Degraded()
	}
	p.CSS = webui.CSS()
	webui.WritePage(w, h.Template, "layout", p)
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
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
	h.page(w, Page{Title: "总览", Active: "overview", Content: "pageOverview",
		PageData: OverviewData{
			UpstreamCount: len(ups),
			NodeCounts:    counts,
			RecentEvents:  eventViews(evs),
			Upstreams:     uviews,
		}})
}

func (h *Handler) upstreams(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ups, _ := h.Store.ListUpstreams(ctx)
	views := make([]UpstreamView, 0, len(ups))
	for _, u := range ups {
		uc, _ := h.Store.CountNodesBySource(ctx, u.Name)
		views = append(views, h.upstreamView(u, uc))
	}
	h.page(w, Page{Title: "上游", Active: "upstreams", Content: "pageUpstreams",
		PageData: UpstreamsData{Upstreams: views, CanFetch: h.Poller != nil, Err: r.URL.Query().Get("err")}})
}

func (h *Handler) nodes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, _ := h.Store.ListNodes(ctx, "")
	views := make([]NodeView, 0, len(rows))
	for _, n := range rows {
		views = append(views, h.nodeView(n))
	}
	h.page(w, Page{Title: "节点", Active: "nodes", Content: "pageNodes", PageData: NodesData{Nodes: views}})
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	evs, _ := h.Store.ListEvents(r.Context(), 200)
	h.page(w, Page{Title: "事件", Active: "events", Content: "pageEvents",
		PageData: EventsData{Events: eventViews(evs)}})
}

func (h *Handler) users(w http.ResponseWriter, r *http.Request) {
	var d UsersData
	if h.Marzban == nil {
		d = UsersData{ErrText: "MARZBAN_API_BASE 未配置, 用户页不可用 (work-6 联调后启用)"}
	} else if users, err := h.Marzban.ListUsers(r.Context()); err != nil {
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
	h.page(w, Page{Title: "用户", Active: "users", Content: "pageUsers", PageData: d})
}

// ---- 管理操作 (7.2) ----

func (h *Handler) upstreamAdd(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	url := r.FormValue("url")
	if name == "" || url == "" {
		http.Redirect(w, r, "/admin/upstreams?err=必填", http.StatusSeeOther)
		return
	}
	if _, err := h.Store.AddUpstream(r.Context(), name, url); err != nil {
		http.Redirect(w, r, "/admin/upstreams?err="+err.Error(), http.StatusSeeOther)
		return
	}
	// 新来源需进 poller 来源清单才能周期拉取: MVP 只入库 + 立即拉一次
	if h.Poller != nil {
		h.Poller.AddSource(sub.Source{Name: name, URL: url})
		_ = h.Poller.PollOnceFor(r.Context(), name)
	}
	http.Redirect(w, r, "/admin/upstreams", http.StatusSeeOther)
}

func (h *Handler) upstreamDelete(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	if err := h.Store.DeleteUpstream(r.Context(), name); err != nil {
		http.Redirect(w, r, "/admin/upstreams?err="+err.Error(), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/upstreams", http.StatusSeeOther)
}

func (h *Handler) upstreamFetch(w http.ResponseWriter, r *http.Request) {
	if h.Poller == nil {
		http.Redirect(w, r, "/admin/upstreams?err=无已配置上游", http.StatusSeeOther)
		return
	}
	name := r.FormValue("name")
	if name != "" {
		if errs := h.Poller.PollOnceFor(r.Context(), name); len(errs) > 0 {
			http.Redirect(w, r, "/admin/upstreams?err="+fmt.Sprint(errs[0]), http.StatusSeeOther)
			return
		}
	} else {
		_ = h.Poller.PollOnce(r.Context())
	}
	http.Redirect(w, r, "/admin/upstreams", http.StatusSeeOther)
}

func (h *Handler) nodeEvict(w http.ResponseWriter, r *http.Request) {
	fp := r.FormValue("fp")
	if _, err := h.Store.Evict(r.Context(), fp); err == nil {
		// 手动摘除立即触发一次 xraygen(5.2 触发链)
		_ = triggerXraygen(r.Context(), h)
	}
	http.Redirect(w, r, "/admin/nodes", http.StatusSeeOther)
}

func (h *Handler) nodeRecover(w http.ResponseWriter, r *http.Request) {
	fp := r.FormValue("fp")
	if _, err := h.Store.Recover(r.Context(), fp); err == nil {
		_ = triggerXraygen(r.Context(), h)
	}
	http.Redirect(w, r, "/admin/nodes", http.StatusSeeOther)
}

// ---- JSON API (7.4): 供轮询(7.3)/未来客户端/监控 ----

func (h *Handler) apiHealthz(w http.ResponseWriter, r *http.Request) {
	degraded := false
	if h.Prober != nil {
		degraded = h.Prober.Degraded()
	}
	status := http.StatusOK
	if degraded {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{"status": "ok", "degraded": degraded})
}

func (h *Handler) apiUpstreams(w http.ResponseWriter, r *http.Request) {
	ups, _ := h.Store.ListUpstreams(r.Context())
	out := make([]map[string]any, 0, len(ups))
	for _, u := range ups {
		uc, _ := h.Store.CountNodesBySource(r.Context(), u.Name)
		out = append(out, map[string]any{
			"name": u.Name, "url_masked": u.URLMasked,
			"last_ok_at": u.LastOKAt.Format(time.RFC3339), "last_err": u.LastErr,
			"node_count": uc,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) apiNodes(w http.ResponseWriter, r *http.Request) {
	rows, _ := h.Store.ListNodes(r.Context(), "")
	views := make([]NodeView, 0, len(rows))
	for _, n := range rows {
		views = append(views, h.nodeView(n))
	}
	writeJSON(w, http.StatusOK, views)
}

func (h *Handler) apiEvents(w http.ResponseWriter, r *http.Request) {
	evs, _ := h.Store.ListEvents(r.Context(), 200)
	writeJSON(w, http.StatusOK, eventViews(evs))
}

func (h *Handler) apiUsers(w http.ResponseWriter, r *http.Request) {
	if h.Marzban == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "marzban not configured"})
		return
	}
	users, err := h.Marzban.ListUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, users)
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
			Kind: e.Kind, KindVar: eventVariant(e.Kind),
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
