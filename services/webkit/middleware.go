package webkit

import (
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// SecurityHeaders 统一安全头中间件(CSP 由各服务传入, 语义同原手撸 wrapper)。
func SecurityHeaders(csp string) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "same-origin")
		c.Next()
	}
}

// SessionPageGuard 页面门槛: 会话无效 → 303 /login。
func SessionPageGuard(s *Session) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, _, ok := s.Value(c.Request); !ok {
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}
		c.Next()
	}
}

// SessionAPIGuard JSON 门槛: 会话无效 → 401 {"error":"unauthorized"}(契约同原 ingest guardAPI)。
func SessionAPIGuard(s *Session) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, _, ok := s.Value(c.Request); !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

// CtxSessionValue gin 上下文键: 存 Session 业务值(user-center 的 sub token)。
const CtxSessionValue = "webkit.sessionValue"

// SessionValueIntoCtx 把会话业务值写入 gin ctx(有会话才进; 供后续 handler 取用)。
func SessionValueIntoCtx(s *Session) gin.HandlerFunc {
	return func(c *gin.Context) {
		if v, _, ok := s.Value(c.Request); ok {
			c.Set(CtxSessionValue, v)
		}
		c.Next()
	}
}

// ClientIP 优先 X-Forwarded-For(语义同原手撸 clientIP; Dokploy 反代场景)。
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// 取最左真实客户端
		if first, _, _ := strings.Cut(xff, ","); first != "" {
			return strings.TrimSpace(first)
		}
		return xff
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// WantsJSON fetch 胶水协商(ADR-0003): wm-ui.js 的 wmAction 带 X-WM-Fetch: json 头;
// 无 JS 环境表单提交不受影响(303 重定向兜底, 渐进增强)。
func WantsJSON(c *gin.Context) bool {
	return c.GetHeader("X-WM-Fetch") == "json"
}
