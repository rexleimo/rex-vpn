// Package accounts 是 authkit(github.com/open-rails/authkit) 账号体系的宿主接线层。
// UC_PG_DSN 未配置时整体禁用(返回 nil), user-center 保持 token 粘贴模式 —— 渐进集成, 不破坏现有部署。
package accounts

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/open-rails/authkit"
	"github.com/open-rails/authkit/iam"
)

// Service 持有 authkit 客户端与 PG 连接池; nil 表示账号体系禁用。
type Service struct {
	Client *authkit.Client
	pool   *pgxpool.Pool
}

// NewFromEnv 按环境变量装配。必需: UC_PG_DSN / UC_TENANT_ID;
// 可选: UC_ISSUER(默认 http://localhost:9200) / UC_KEYS_PATH / UC_DEV_KEYS=1(开发态自动生成签名密钥)。
func NewFromEnv(ctx context.Context) (*Service, error) {
	dsn := os.Getenv("UC_PG_DSN")
	if dsn == "" {
		return nil, nil // 账号体系未启用: 调用方按 nil 走旧 token 模式
	}
	tenantID, err := iam.ParseTenantID(os.Getenv("UC_TENANT_ID"))
	if err != nil {
		return nil, fmt.Errorf("UC_TENANT_ID: %w", err)
	}
	issuer := os.Getenv("UC_ISSUER")
	if issuer == "" {
		issuer = "http://localhost:9200"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pg ping: %w", err)
	}
	auth, err := authkit.New(ctx, authkit.Config{
		DefaultTenantID: tenantID,
		Database:        authkit.DatabaseConfig{Schema: "authkit"},
		Token: authkit.TokenConfig{
			Issuer:          issuer,
			IssuedAudiences: []string{"rex-vpn"},
		},
		Keys: authkit.KeysConfig{
			Path:                  os.Getenv("UC_KEYS_PATH"),
			AllowEphemeralDevKeys: os.Getenv("UC_DEV_KEYS") == "1",
		},
		HTTP: &authkit.HTTPConfig{DirectPeerIP: true},
		// v1 策略: 无邮件通道 → 注册验证关闭; 自助注册关闭(admin 建号, WI-5 换邀请制)。
		Registration: authkit.RegistrationConfig{
			Verification:        iam.RegistrationVerificationNone,
			NativeUserMode:      iam.RegistrationModeClosed,
			AllowMissingSenders: true,
		},
		Roles: authkit.NewRoles(),
	}, authkit.Deps{Postgres: pool})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("authkit new: %w", err)
	}
	if err := auth.Start(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("authkit start: %w", err)
	}
	return &Service{Client: auth, pool: pool}, nil
}

// Close 停后台作业并关连接池。
func (s *Service) Close(ctx context.Context) {
	if s == nil {
		return
	}
	_ = s.Client.Close(ctx)
	s.pool.Close()
}
