package web

// actions.go: 管理操作端点(上游 CRUD + 手动拉取 + 节点摘除/回池)。
// 双模式契约(ADR-0003/0004): wm-ui.js fetch(X-WM-Fetch: json) → JSON {ok,flash|error};
// 无 JS 表单 → 303 + ?err= 兜底。错误全透明(不吞 error)。

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"vpnmaster/ingest/internal/sub"
	"vpnmaster/webkit"
)

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
	if h.Poller != nil {
		if !h.Poller.UpdateSource(name, urlStr) {
			h.Poller.AddSource(sub.Source{Name: name, URL: urlStr}) // 旧版遗留"未跟踪"行 → 编辑即重新入列(URL 已同步落库, 后续重启不再需要)
			_ = h.Poller.PollOnceFor(c.Request.Context(), name)
			replyOK(c, upsRedirect, "已更新并重新加入拉取 "+name)
			return
		}
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

const nodesRedirect = "/admin/nodes"

func (h *Handler) nodeEvict(c *gin.Context) {
	fp := c.PostForm("fp")
	if _, err := h.Store.Evict(c.Request.Context(), fp); err != nil {
		replyErr(c, nodesRedirect, http.StatusInternalServerError, "摘除失败: "+err.Error())
		return
	}
	if err := triggerXraygen(c.Request.Context(), h); err != nil {
		replyErr(c, nodesRedirect, http.StatusBadGateway, "已摘除但 xray 同步失败: "+err.Error())
		return
	}
	replyOK(c, nodesRedirect, "已摘除并同步出口")
}

func (h *Handler) nodeRecover(c *gin.Context) {
	fp := c.PostForm("fp")
	if _, err := h.Store.Recover(c.Request.Context(), fp); err != nil {
		replyErr(c, nodesRedirect, http.StatusInternalServerError, "回池失败: "+err.Error())
		return
	}
	if err := triggerXraygen(c.Request.Context(), h); err != nil {
		replyErr(c, nodesRedirect, http.StatusBadGateway, "已回池但 xray 同步失败: "+err.Error())
		return
	}
	replyOK(c, nodesRedirect, "已回池并同步出口")
}
