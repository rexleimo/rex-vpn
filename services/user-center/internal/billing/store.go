// store.go: PG 实现(waffo 事件幂等表 + 订阅镜像) + 测试用内存实现。
package billing

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const ddl = `CREATE TABLE IF NOT EXISTS uc_waffo_events(
	event_id   text PRIMARY KEY,
	event_type text NOT NULL,
	seen_at    timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS uc_subscriptions(
	account_id  text PRIMARY KEY,
	order_id    text NOT NULL DEFAULT '',
	plan_name   text NOT NULL DEFAULT '',
	status      text NOT NULL DEFAULT 'active',
	period_end  timestamptz,
	updated_at  timestamptz NOT NULL DEFAULT now()
)`

// EnsureSchema 幂等建表(与 uc_account_links 同 public schema)。
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, ddl)
	return err
}

// PGStore 是 Store 的 Postgres 实现。
type PGStore struct {
	pool *pgxpool.Pool
}

func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

func (g *PGStore) Seen(eventID string) (bool, error) {
	var one int
	err := g.pool.QueryRow(context.Background(),
		`SELECT 1 FROM uc_waffo_events WHERE event_id = $1`, eventID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (g *PGStore) MarkSeen(eventID, eventType string) error {
	_, err := g.pool.Exec(context.Background(),
		`INSERT INTO uc_waffo_events(event_id, event_type) VALUES($1,$2) ON CONFLICT (event_id) DO NOTHING`,
		eventID, eventType)
	return err
}

func (g *PGStore) UpsertSub(ctx context.Context, row SubRow) error {
	_, err := g.pool.Exec(ctx, `INSERT INTO uc_subscriptions(account_id, order_id, plan_name, status, period_end, updated_at)
		VALUES($1,$2,$3,$4,$5,now())
		ON CONFLICT (account_id) DO UPDATE SET
			order_id = EXCLUDED.order_id,
			plan_name = CASE WHEN EXCLUDED.plan_name <> '' THEN EXCLUDED.plan_name ELSE uc_subscriptions.plan_name END,
			status = EXCLUDED.status,
			period_end = COALESCE(EXCLUDED.period_end, uc_subscriptions.period_end),
			updated_at = now()`,
		row.AccountID, row.OrderID, row.PlanName, row.Status, nullTime(row.PeriodEnd))
	return err
}

func (g *PGStore) GetSub(ctx context.Context, accountID string) (SubRow, bool, error) {
	var r SubRow
	var pe *time.Time
	err := g.pool.QueryRow(ctx,
		`SELECT account_id, order_id, plan_name, status, period_end, updated_at FROM uc_subscriptions WHERE account_id = $1`,
		accountID).Scan(&r.AccountID, &r.OrderID, &r.PlanName, &r.Status, &pe, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SubRow{}, false, nil
	}
	if err != nil {
		return SubRow{}, false, err
	}
	if pe != nil {
		r.PeriodEnd = *pe
	}
	return r, true, nil
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// MemStore 是测试/降级实现。
type MemStore struct {
	mu   sync.Mutex
	seen map[string]string
	subs map[string]SubRow
}

func NewMemStore() *MemStore {
	return &MemStore{seen: map[string]string{}, subs: map[string]SubRow{}}
}

func (m *MemStore) Seen(id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.seen[id]
	return ok, nil
}

func (m *MemStore) MarkSeen(id, typ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seen[id] = typ
	return nil
}

func (m *MemStore) UpsertSub(ctx context.Context, row SubRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if row.AccountID == "" {
		return errors.New("billing: sub row without account")
	}
	m.subs[row.AccountID] = row
	return nil
}

func (m *MemStore) GetSub(ctx context.Context, accountID string) (SubRow, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.subs[accountID]
	return r, ok, nil
}
