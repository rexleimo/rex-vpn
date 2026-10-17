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
	Client           *authkit.Client
	pool             *pgxpool.Pool
	AdminRole        iam.Role // root 管理角色(见 admin.go), 在 NewFromEnv 声明
	RegistrationOpen bool     // UC_REGISTRATION=open: 登录页展示自助注册入口
}

// SelfRegistrationOpen 供 web 层门槛判断(接口化, 避免类型接触字段)。
func (s *Service) SelfRegistrationOpen() bool { return s != nil && s.RegistrationOpen }

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
	// 权限模型: root 上的 admin 角色(用户目录全权), 供运营页与种子管理员。
	roles := authkit.NewRoles()
	adminRole := roles.Root.Role(AdminRoleName, roles.Root.Users.All())
	// 注册策略: 默认 closed(admin 建号, 与 v1 安全姿态一致); UC_REGISTRATION=open 开自助注册。
	// 无邮件通道下 Verification 恒为 none(注册即激活), 开放注册有垃圾号风险, 生产不自开。
	regMode := iam.RegistrationModeClosed
	if os.Getenv("UC_REGISTRATION") == "open" {
		regMode = iam.RegistrationModeOpen
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
		// v1 策略: 无邮件通道 → 注册验证关闭; 自助注册默认关(admin 建号, UC_REGISTRATION=open 可开)。
		Registration: authkit.RegistrationConfig{
			Verification:        iam.RegistrationVerificationNone,
			NativeUserMode:      regMode,
			AllowMissingSenders: true,
		},
		// v1 无邮件/TOTP 注册 UX: 2FA 整体关。注意 admin 角色的 Users.All() 授权
		// 会派生 RequiresMFA(见 internal/rbac/schema.go), 不关则管理员登录被 enrollment 死锁。
		// 后续强化: 接邮件通道后改 optional, 管理员单独 required+TOTP。
		TwoFactor: authkit.TwoFactorConfig{Mode: iam.TwoFactorDisabled},
		Roles:     roles,
	}, authkit.Deps{Postgres: pool})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("authkit new: %w", err)
	}
	if err := auth.Start(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("authkit start: %w", err)
	}
	return &Service{Client: auth, pool: pool, AdminRole: adminRole, RegistrationOpen: regMode == iam.RegistrationModeOpen}, nil
}

// Close 停后台作业并关连接池。
func (s *Service) Close(ctx context.Context) {
	if s == nil {
		return
	}
	_ = s.Client.Close(ctx)
	s.pool.Close()
}

// Pool 暴露底层 pgx 池供同进程其他域(如 billing)复用同一连接池。
func (s *Service) Pool() *pgxpool.Pool { return s.pool }
