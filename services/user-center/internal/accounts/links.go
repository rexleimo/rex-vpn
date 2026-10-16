// links.go: 账号 ↔ Marzban 订阅 token 的服务端映射(WI-4)。
// token 只在服务端与 PG 里流转, 用户界面从"每次粘贴 token"降为"一次性绑定"。
package accounts

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/open-rails/authkit/iam"
)

const linksDDL = `CREATE TABLE IF NOT EXISTS uc_account_links (
	account_id    text PRIMARY KEY,
	marzban_token text NOT NULL,
	updated_at    timestamptz NOT NULL DEFAULT now()
)`

// EnsureLinksTable 幂等建映射表(public schema, 与 authkit schema 隔离)。
func (s *Service) EnsureLinksTable(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, linksDDL)
	return err
}

// LinkToken 查账号绑定的 Marzban token; ok=false 表示尚未绑定。
func (s *Service) LinkToken(ctx context.Context, accountID string) (string, bool, error) {
	var tok string
	err := s.pool.QueryRow(ctx,
		`SELECT marzban_token FROM uc_account_links WHERE account_id = $1`, accountID).Scan(&tok)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return tok, true, nil
}

// SetLink 绑定/换绑账号的 Marzban token。
func (s *Service) SetLink(ctx context.Context, accountID, token string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO uc_account_links (account_id, marzban_token)
		VALUES ($1, $2) ON CONFLICT (account_id) DO UPDATE
		SET marzban_token = EXCLUDED.marzban_token, updated_at = now()`, accountID, token)
	return err
}

// ClearLink 解绑(删号/换订阅时用)。
func (s *Service) ClearLink(ctx context.Context, accountID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM uc_account_links WHERE account_id = $1`, accountID)
	return err
}

// AccountEmail 取账号邮箱(绑定页/概览展示用)。
func (s *Service) AccountEmail(ctx context.Context, accountID string) (string, error) {
	u, err := s.Client.User(ctx, iam.UserByID(accountID))
	if err != nil {
		return "", err
	}
	if u.Email != nil {
		return *u.Email, nil
	}
	return u.Username, nil
}
