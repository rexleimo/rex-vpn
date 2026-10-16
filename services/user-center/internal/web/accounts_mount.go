// accounts_mount.go: authkit HTTP 面挂载(WI-2)。未接账号体系时全部为 no-op。
package web

import (
	"log"

	"github.com/gin-gonic/gin"
	authkitgin "github.com/open-rails/authkit/adapters/gin"
	"vpnmaster/user-center/internal/accounts"
)

// AttachAccounts 注入账号体系服务(可 nil)。在 Router() 之前调用。
func (s *Server) AttachAccounts(a *accounts.Service) { s.accounts = a }

// mountAccounts 把 authkit 的全部认证路由注册到 gin 引擎。
func (s *Server) mountAccounts(e *gin.Engine) {
	if s.accounts == nil {
		return
	}
	if err := authkitgin.Mount(e, s.accounts.Client); err != nil {
		log.Fatalf("authkit mount: %v", err)
	}
}
