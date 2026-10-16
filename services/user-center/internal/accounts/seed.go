// seed.go: 宿主侧建号(自助注册已配 closed)。
// 启动种子: UC_ADMIN_EMAIL/UC_ADMIN_PASSWORD 存在时幂等建第一个管理员;
// CreatePasswordUser 供 WI-5 的 admin「+添加用户」复用。
package accounts

import (
	"context"
	"errors"
	"strings"

	"github.com/open-rails/authkit/iam"
)

// CreatePasswordUser 建邮箱+密码账号(邮箱视为已验证: 运营线下交付口令)。
// username 为空时从邮箱本地部分派生(authkit 默认用户名最小长度 4, 需清洗到 [a-z0-9])。
func (s *Service) CreatePasswordUser(ctx context.Context, email, username, password string) (string, error) {
	if email == "" || password == "" {
		return "", errors.New("email 与 password 必填")
	}
	if username == "" {
		username = usernameFromEmail(email)
	}
	u, err := s.Client.CreateUser(ctx, iam.NewUser{
		Email:         email,
		Username:      username,
		Password:      password,
		EmailVerified: true,
	})
	if err != nil {
		return "", err
	}
	return u.ID, nil
}

// usernameFromEmail 取邮箱本地部分的 [a-z0-9] 前 20 字符; 不足 4 位补 "user"。
func usernameFromEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at < 0 {
		at = len(email)
	}
	var b strings.Builder
	for _, r := range strings.ToLower(email[:at]) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
		if b.Len() >= 20 {
			break
		}
	}
	name := b.String()
	if len(name) < 4 {
		name += "user"
	}
	return name
}

// EnsurePasswordUser 幂等: 邮箱已存在则返回现有账号 id, 否则创建。
func (s *Service) EnsurePasswordUser(ctx context.Context, email, password string) (string, error) {
	if existing, err := s.Client.User(ctx, iam.UserByEmail(email)); err == nil {
		return existing.ID, nil
	}
	return s.CreatePasswordUser(ctx, email, "", password)
}

// UserExists 供测试与 admin 校验。
func (s *Service) UserExists(ctx context.Context, email string) bool {
	_, err := s.Client.User(ctx, iam.UserByEmail(email))
	return err == nil
}
