// accounts_mount.go: authkit 账号体系的 web 层接线(WI-2/WI-3)。未接账号体系时全部为 no-op。
package web

import (
	"context"
	"log"

	"github.com/gin-gonic/gin"
	authkitgin "github.com/open-rails/authkit/adapters/gin"
	"vpnmaster/user-center/internal/accounts"
)

// AccountBackend 是 web 层用到的账号体系能力(可 mock, 契约测试不依赖 PG)。
type AccountBackend interface {
	Login(ctx context.Context, identifier, password, clientIP string) (accounts.Auth, error)
	LinkToken(ctx context.Context, accountID string) (string, bool, error)
	SetLink(ctx context.Context, accountID, token string) error
	AccountEmail(ctx context.Context, accountID string) (string, error)
	IsAdmin(ctx context.Context, userID string) bool
	ListAccounts(ctx context.Context, search string) ([]accounts.AdminUser, error)
	CreatePasswordUser(ctx context.Context, email, username, password string) (string, error)
	BanAccount(ctx context.Context, userID, reason string) error
	UnbanAccount(ctx context.Context, userID string) error
	DeleteAccount(ctx context.Context, userID string) error
	ResetPassword(ctx context.Context, userID, password string) error
	GrantAdmin(ctx context.Context, email string) error
}

// AttachAccounts 注入账号体系服务(nil 或不注入 = 保持 token 粘贴模式)。在 Router() 之前调用。
func (s *Server) AttachAccounts(a *accounts.Service) {
	if a != nil {
		s.accounts = a
	}
}

// mountAccounts 把 authkit 的全部认证路由注册到 gin 引擎。
func (s *Server) mountAccounts(e *gin.Engine) {
	svc, ok := s.accounts.(*accounts.Service)
	if !ok {
		return
	}
	if err := authkitgin.Mount(e, svc.Client); err != nil {
		log.Fatalf("authkit mount: %v", err)
	}
}
