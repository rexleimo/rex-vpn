// admin.go: 宿主管理面(WI-5)。以 iam.SystemIdentity() 行使主机权限,
// 供 user-center 的 /admin/users 页调用; 权限门槛(IsAdmin)在 web 层做。
package accounts

import (
	"context"
	"errors"
	"fmt"

	"github.com/open-rails/authkit/iam"
)

// AdminRoleName 是授予运营的管理角色名。
const AdminRoleName = "admin"

// GrantAdmin 给邮箱账号授予 root admin 角色(幂等)。
func (s *Service) GrantAdmin(ctx context.Context, email string) error {
	_, err := s.Client.EnsureUserRole(ctx, iam.RootGroup(), iam.UserByEmail(email), s.AdminRole)
	return err
}

// IsAdmin 判定账号是否持有 root admin 角色。
func (s *Service) IsAdmin(ctx context.Context, userID string) bool {
	roles, err := s.Client.GroupRoles(ctx, iam.RootGroup(), []iam.Subject{iam.UserSubject(userID)})
	if err != nil {
		return false
	}
	for _, r := range roles {
		if r.Name() == AdminRoleName {
			return true
		}
	}
	return false
}

// AdminUser 是管理页的一行用户视图。
type AdminUser struct {
	ID        string
	Email     string
	Username  string
	Status    string // active | banned | deleted
	Admin     bool
	LastLogin string
	CreatedAt string
}

// ListAccounts 列出账号(搜索=邮箱/用户名子串)。
func (s *Service) ListAccounts(ctx context.Context, search string) ([]AdminUser, error) {
	page, err := s.Client.ListUsers(ctx, iam.UserQuery{
		Search: search,
		Status: iam.UserStatusAny,
		Page:   iam.PageRequest{Limit: 50},
	})
	if err != nil {
		return nil, err
	}
	ids := make([]iam.Subject, 0, len(page.Items))
	for _, e := range page.Items {
		ids = append(ids, iam.UserSubject(e.ID))
	}
	out := make([]AdminUser, 0, len(page.Items))
	for _, e := range page.Items {
		u := AdminUser{ID: e.ID, Username: e.Username, Status: "active", CreatedAt: e.CreatedAt.Format("2006-01-02")}
		if e.Email != nil {
			u.Email = *e.Email
		}
		if e.LastLogin != nil {
			u.LastLogin = e.LastLogin.Format("2006-01-02 15:04")
		}
		switch {
		case e.DeletedAt != nil:
			u.Status = "deleted"
		case e.Ban != nil:
			u.Status = "banned"
		}
		out = append(out, u)
	}
	// admin 标记: 一次批量查 root 角色(GroupRoles 返回 map[Subject]Role 单值)。
	if len(ids) > 0 {
		if roles, err := s.Client.GroupRoles(ctx, iam.RootGroup(), ids); err == nil {
			for i := range out {
				if r, ok := roles[iam.UserSubject(out[i].ID)]; ok && r.Name() == AdminRoleName {
					out[i].Admin = true
				}
			}
		}
	}
	return out, nil
}

// BanAccount 禁用(可逆)。
func (s *Service) BanAccount(ctx context.Context, userID, reason string) error {
	return s.Client.Ban(ctx, iam.SystemIdentity(), userID, iam.Ban{Reason: reason})
}

// UnbanAccount 恢复。
func (s *Service) UnbanAccount(ctx context.Context, userID string) error {
	return s.Client.Unban(ctx, iam.SystemIdentity(), userID)
}

// DeleteAccount 软删(30 天可恢复, authkit 语义)。
func (s *Service) DeleteAccount(ctx context.Context, userID string) error {
	rs, err := s.Client.DeleteUsers(ctx, iam.SystemIdentity(), []string{userID})
	if err != nil {
		return err
	}
	return opResultErr(rs)
}

// ResetPassword 运营重置口令(用户忘密且无邮件通道时的唯一出路)。
func (s *Service) ResetPassword(ctx context.Context, userID, password string) error {
	_, err := s.Client.UpdateUser(ctx, iam.SystemIdentity(), userID, iam.UserUpdate{Password: &password})
	return err
}

func opResultErr(rs []iam.OpResult) error {
	for _, r := range rs {
		if r.Err != nil {
			return fmt.Errorf("%v", r.Err)
		}
	}
	if len(rs) == 0 {
		return errors.New("no result")
	}
	return nil
}
