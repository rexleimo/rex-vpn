// accounts_handlers.go: 邮箱密码登录 + 账号↔订阅绑定(WI-3/WI-4)。
// 会话约定: 账号模式 session value = "acct:<authkit-userID>"; Marzban token 存服务端映射表, 浏览器不再携带。
package web

import (
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"vpnmaster/webkit"
)

const acctPrefix = "acct:"

func acctID(value string) (string, bool) {
	if !strings.HasPrefix(value, acctPrefix) {
		return "", false
	}
	id := strings.TrimPrefix(value, acctPrefix)
	return id, id != ""
}

// sessionValue 取当前 session value(两种模式通用)。
func (s *Server) sessionValue(r *http.Request) (string, bool) {
	v, _, ok := s.session.Value(r)
	return v, ok
}

// resolveToken 把会话解析为 Marzban token: legacy=值本身; 账号=映射表查询。
func (s *Server) resolveToken(r *http.Request) (string, bool) {
	v, ok := s.sessionValue(r)
	if !ok {
		return "", false
	}
	if id, isAcct := acctID(v); isAcct && s.accounts != nil {
		tok, found, err := s.accounts.LinkToken(r.Context(), id)
		if err != nil || !found {
			return "", false
		}
		return tok, true
	}
	return v, true
}

// accountIDIfBound 供 auth() 门槛: 账号会话未绑定订阅时返回其 accountID → 303 /bind。
func (s *Server) accountUnbound(c *gin.Context) (string, bool) {
	if s.accounts == nil {
		return "", false
	}
	v, ok := s.sessionValue(c.Request)
	if !ok {
		return "", false
	}
	id, isAcct := acctID(v)
	if !isAcct {
		return "", false
	}
	_, found, err := s.accounts.LinkToken(c.Request.Context(), id)
	if err == nil && found {
		return "", false
	}
	return id, true
}

// loginAcct 账号模式登录分支: authkit 校验 → 种 acct 会话。返回是否已处理。
func (s *Server) loginAcct(c *gin.Context, jsonMode bool, replyFail func(string)) bool {
	if s.accounts == nil {
		return false
	}
	identifier := strings.TrimSpace(c.PostForm("identifier"))
	password := c.PostForm("password")
	if identifier == "" || password == "" {
		replyFail("请输入邮箱和密码")
		return true
	}
	a, err := s.accounts.Login(c.Request.Context(), identifier, password, webkit.ClientIP(c.Request))
	if err != nil {
		// 对外不泄露账号存在性; 细节进服务端日志
		s.logf("login failed %q from %s: %v", identifier, webkit.ClientIP(c.Request), err)
		replyFail("账号或密码错误")
		return true
	}
	s.session.Revoke(c.Writer, c.Request)
	s.session.Issue(c.Writer, acctPrefix+a.UserID)
	if jsonMode {
		c.JSON(http.StatusOK, gin.H{"ok": true, "redirect": "/dashboard"})
		return true
	}
	c.Redirect(http.StatusSeeOther, "/dashboard")
	return true
}

// bindOnly 绑定页门槛: 必须是已登录的 acct 会话(未绑定也可进); 其他一律回 /login 或 dashboard。
func (s *Server) bindOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		v, ok := s.sessionValue(c.Request)
		if !ok {
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}
		if _, isAcct := acctID(v); !isAcct {
			c.Redirect(http.StatusSeeOther, "/dashboard") // legacy 会话不需要绑定页
			c.Abort()
			return
		}
		c.Next()
	}
}

// bindPage 首次绑定: 已登录账号但无订阅映射时进入。
func (s *Server) bindPage(c *gin.Context) {
	v, _ := s.sessionValue(c.Request)
	id, _ := acctID(v)
	email, _ := s.accounts.AccountEmail(c.Request.Context(), id)
	s.page(c, Page{Title: "绑定订阅", Active: "dashboard", Content: "bind",
		PageData: map[string]any{"Email": email, "Err": c.Query("err") != ""}})
}

// bindPost 保存绑定: 先经 Marzban 校验 token 有效再落映射表。
func (s *Server) bindPost(c *gin.Context) {
	jsonMode := webkit.WantsJSON(c)
	v, _ := s.sessionValue(c.Request)
	id, _ := acctID(v)
	token := strings.TrimSpace(c.PostForm("token"))
	fail := func(msg string) {
		if jsonMode {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": msg})
			return
		}
		c.Redirect(http.StatusSeeOther, "/bind?err="+url.QueryEscape(msg))
	}
	if token == "" {
		fail("token 不能为空")
		return
	}
	if u, err := s.Marzban.GetUserByToken(c.Request.Context(), token); err != nil || u.Username == "" {
		fail("token 无效或已过期")
		return
	}
	if err := s.accounts.SetLink(c.Request.Context(), id, token); err != nil {
		fail("保存失败")
		return
	}
	if jsonMode {
		c.JSON(http.StatusOK, gin.H{"ok": true, "redirect": "/dashboard"})
		return
	}
	c.Redirect(http.StatusSeeOther, "/dashboard")
}

func (s *Server) logf(format string, args ...any) { log.Printf(format, args...) }
