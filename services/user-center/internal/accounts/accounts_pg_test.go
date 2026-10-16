// accounts_pg_test.go: 真实 PostgreSQL + authkit 的端到端契约(建号→登录→映射)。
// 无 UC_PG_DSN 时 skip, CI 不受影响; 本地 source deploy/.env 后可跑。
package accounts

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	if os.Getenv("UC_PG_DSN") == "" {
		t.Skip("UC_PG_DSN 未配置, 跳过 PG 集成测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	svc, err := NewFromEnv(ctx)
	if err != nil {
		t.Fatalf("NewFromEnv: %v", err)
	}
	t.Cleanup(func() { svc.Close(context.Background()) })
	if err := svc.EnsureLinksTable(ctx); err != nil {
		t.Fatalf("EnsureLinksTable: %v", err)
	}
	return svc
}

func TestPGCreateLoginAndLink(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	email := fmt.Sprintf("pgtest-%d@example.test", time.Now().UnixNano())
	pw := "Corr3ct-Horse-Battery!"

	id, err := svc.CreatePasswordUser(ctx, email, "", pw)
	if err != nil {
		t.Fatalf("CreatePasswordUser: %v", err)
	}
	if !svc.UserExists(ctx, email) {
		t.Fatal("UserExists after create = false")
	}

	// 幂等种子: 同邮箱返回同 id
	id2, err := svc.EnsurePasswordUser(ctx, email, "ignored")
	if err != nil || id2 != id {
		t.Fatalf("EnsurePasswordUser = (%q,%v), want existing %q", id2, err, id)
	}

	auth, err := svc.Login(ctx, email, pw, "127.0.0.1")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if auth.UserID != id {
		t.Fatalf("Login UserID = %q, want %q", auth.UserID, id)
	}
	if auth.Email != email {
		t.Errorf("Login Email = %q, want %q", auth.Email, email)
	}

	if _, err := svc.Login(ctx, email, pw+"wrong", "127.0.0.1"); err == nil {
		t.Fatal("Login with wrong password must fail")
	}

	// 映射表读写
	if _, found, err := svc.LinkToken(ctx, id); err != nil || found {
		t.Fatalf("LinkToken before set = (%v,%v), want not found", found, err)
	}
	if err := svc.SetLink(ctx, id, "marzban-token-abc"); err != nil {
		t.Fatalf("SetLink: %v", err)
	}
	tok, found, err := svc.LinkToken(ctx, id)
	if err != nil || !found || tok != "marzban-token-abc" {
		t.Fatalf("LinkToken = (%q,%v,%v)", tok, found, err)
	}
	if err := svc.ClearLink(ctx, id); err != nil {
		t.Fatalf("ClearLink: %v", err)
	}
	if _, found, _ := svc.LinkToken(ctx, id); found {
		t.Fatal("link still present after ClearLink")
	}

	em, err := svc.AccountEmail(ctx, id)
	if err != nil || em != email {
		t.Fatalf("AccountEmail = (%q,%v)", em, err)
	}
}

// TestPGAdminSurface: 授权→列表→禁用→恢复→重置密码→删除 全链路(真 authkit)。
func TestPGAdminSurface(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	email := fmt.Sprintf("pgadmin-%d@example.test", time.Now().UnixNano())
	pw := "Adm1n-Test-Pass!"

	id, err := svc.CreatePasswordUser(ctx, email, "", pw)
	if err != nil {
		t.Fatalf("CreatePasswordUser: %v", err)
	}
	if svc.IsAdmin(ctx, id) {
		t.Fatal("fresh user must not be admin")
	}
	if err := svc.GrantAdmin(ctx, email); err != nil {
		t.Fatalf("GrantAdmin: %v", err)
	}
	if !svc.IsAdmin(ctx, id) {
		t.Fatal("IsAdmin after GrantAdmin = false")
	}

	find := func(want string, pred func(AdminUser) bool) AdminUser {
		t.Helper()
		list, err := svc.ListAccounts(ctx, email)
		if err != nil {
			t.Fatalf("ListAccounts: %v", err)
		}
		for _, u := range list {
			if u.ID == id {
				if !pred(u) {
					t.Fatalf("%s: entry %+v", want, u)
				}
				return u
			}
		}
		t.Fatalf("%s: user %s not in list", want, id)
		return AdminUser{}
	}
	find("admin flag", func(u AdminUser) bool { return u.Admin && u.Status == "active" })

	if err := svc.BanAccount(ctx, id, "e2e"); err != nil {
		t.Fatalf("BanAccount: %v", err)
	}
	find("banned", func(u AdminUser) bool { return u.Status == "banned" })
	if _, err := svc.Login(ctx, email, pw, "127.0.0.1"); err == nil {
		t.Fatal("banned account login must fail")
	}
	if err := svc.UnbanAccount(ctx, id); err != nil {
		t.Fatalf("UnbanAccount: %v", err)
	}
	find("unbanned", func(u AdminUser) bool { return u.Status == "active" })

	newPw := "Rotated-9Pass!"
	if err := svc.ResetPassword(ctx, id, newPw); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if _, err := svc.Login(ctx, email, pw, "127.0.0.1"); err == nil {
		t.Fatal("old password must stop working")
	}
	if _, err := svc.Login(ctx, email, newPw, "127.0.0.1"); err != nil {
		t.Fatalf("login with reset password: %v", err)
	}

	if err := svc.DeleteAccount(ctx, id); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	find("deleted", func(u AdminUser) bool { return u.Status == "deleted" })
}
