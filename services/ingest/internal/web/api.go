package web

// api.go: JSON API 端点(7.4), 供页面轮询(7.3)/未来客户端/监控。
// 契约纪律(ADR-0004): 数组体保持旧契约(chaos/load-50 断言不破), 无参=全量;
// 分页总数走 X-Total-Count header; 事件游标 = EventView.ID。

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

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
	if q := strings.ToLower(c.Query("q")); q != "" {
		var kept []User
		for _, u := range users {
			if strings.Contains(strings.ToLower(u.Username), q) || strings.Contains(strings.ToLower(u.ID), q) {
				kept = append(kept, u)
			}
		}
		users = kept
	}
	writeJSON(c.Writer, http.StatusOK, users)
}
