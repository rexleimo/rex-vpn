// admin_users.go: 运营「用户管理」页(WI-5)。账号体系内建, 取代 ingest 只读页。
// 交互契约与 ingest admin 一致: form[data-wm-act] JSON 提交, 破坏性操作 wmConfirmAct。
package web

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"vpnmaster/user-center/internal/accounts"
	"vpnmaster/webkit"
)

// adminOnly 管理面门槛: 必须是 admin 角色的 acct 会话。
func (s *Server) adminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.accounts == nil {
			c.Redirect(http.StatusSeeOther, "/dashboard")
			c.Abort()
			return
		}
		v, ok := s.sessionValue(c.Request)
		if !ok {
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}
		id, isAcct := acctID(v)
		if !isAcct || !s.accounts.IsAdmin(c.Request.Context(), id) {
			c.Redirect(http.StatusSeeOther, "/dashboard")
			c.Abort()
			return
		}
		c.Next()
	}
}

// selfActionGuard 禁止对自己 ban/delete(防锁死)。
func (s *Server) selfActionGuard(c *gin.Context, targetID string) bool {
	v, _ := s.sessionValue(c.Request)
	if id, isAcct := acctID(v); isAcct && id == targetID {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "不能对自己执行该操作"})
		return false
	}
	return true
}

type adminUsersData struct {
	Users  []accounts.AdminUser
	Search string
	SelfID string
}

func (s *Server) adminUsersPage(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	users, err := s.accounts.ListAccounts(c.Request.Context(), q)
	if err != nil {
		s.logf("ListAccounts: %v", err)
	}
	v, _ := s.sessionValue(c.Request)
	self, _ := acctID(v)
	s.page(c, Page{Title: "用户管理", Active: "admin", Content: "adminUsers",
		PageData: adminUsersData{Users: users, Search: q, SelfID: self}})
}

// actResp JSON/303 双模式回复(ADR-0004)。
func actResp(c *gin.Context, ok bool, flash, errMsg string) {
	if webkit.WantsJSON(c) {
		if ok {
			c.JSON(http.StatusOK, gin.H{"ok": true, "flash": flash})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": errMsg})
		return
	}
	if ok {
		c.Redirect(http.StatusSeeOther, "/admin/users?msg="+url.QueryEscape(flash))
		return
	}
	c.Redirect(http.StatusSeeOther, "/admin/users?msg="+url.QueryEscape(errMsg))
}

func (s *Server) adminUserCreate(c *gin.Context) {
	email := strings.TrimSpace(c.PostForm("email"))
	pw := c.PostForm("password")
	if email == "" || len(pw) < 8 {
		actResp(c, false, "", "邮箱必填, 密码至少 8 位")
		return
	}
	if _, err := s.accounts.CreatePasswordUser(c.Request.Context(), email, "", pw); err != nil {
		s.logf("create user %s: %v", email, err)
		actResp(c, false, "", "创建失败(邮箱可能已存在)")
		return
	}
	actResp(c, true, "已添加 "+email, "")
}

func (s *Server) adminUserBan(c *gin.Context) {
	id := c.PostForm("id")
	if !s.selfActionGuard(c, id) {
		return
	}
	if err := s.accounts.BanAccount(c.Request.Context(), id, "运营禁用"); err != nil {
		actResp(c, false, "", "禁用失败")
		return
	}
	actResp(c, true, "已禁用", "")
}

func (s *Server) adminUserUnban(c *gin.Context) {
	id := c.PostForm("id")
	if err := s.accounts.UnbanAccount(c.Request.Context(), id); err != nil {
		actResp(c, false, "", "恢复失败")
		return
	}
	actResp(c, true, "已恢复", "")
}

func (s *Server) adminUserDelete(c *gin.Context) {
	id := c.PostForm("id")
	if !s.selfActionGuard(c, id) {
		return
	}
	if err := s.accounts.DeleteAccount(c.Request.Context(), id); err != nil {
		actResp(c, false, "", "删除失败")
		return
	}
	actResp(c, true, "已删除(30 天内可恢复)", "")
}

func (s *Server) adminUserResetPw(c *gin.Context) {
	id := c.PostForm("id")
	pw := c.PostForm("password")
	if len(pw) < 8 {
		actResp(c, false, "", "新密码至少 8 位")
		return
	}
	if err := s.accounts.ResetPassword(c.Request.Context(), id, pw); err != nil {
		actResp(c, false, "", "重置失败")
		return
	}
	actResp(c, true, "密码已重置", "")
}

func (s *Server) adminUserGrant(c *gin.Context) {
	email := strings.TrimSpace(c.PostForm("email"))
	if email == "" {
		actResp(c, false, "", "缺少邮箱")
		return
	}
	if err := s.accounts.GrantAdmin(c.Request.Context(), email); err != nil {
		actResp(c, false, "", "授权失败")
		return
	}
	actResp(c, true, "已授予管理员 "+email, "")
}
