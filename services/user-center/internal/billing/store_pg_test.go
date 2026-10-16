// store_pg_test.go: billing PGStore 真库集成(UC_PG_DSN 存在才跑)。
package billing

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func pgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("UC_PG_DSN")
	if dsn == "" {
		t.Skip("UC_PG_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := EnsureSchema(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestPGStoreIdempotencyAndMirror(t *testing.T) {
	pool := pgPool(t)
	ctx := context.Background()
	acct := "pgtest-billing-" + time.Now().Format("150405")
	st := NewPGStore(pool)

	seen, err := st.Seen("evt-" + acct)
	if err != nil || seen {
		t.Fatalf("seen %v err %v", seen, err)
	}
	if err := st.MarkSeen("evt-"+acct, "subscription.activated"); err != nil {
		t.Fatal(err)
	}
	seen, _ = st.Seen("evt-" + acct)
	if !seen {
		t.Fatal("event should be seen")
	}
	if err := st.MarkSeen("evt-"+acct, "subscription.activated"); err != nil {
		t.Fatal("MarkSeen must be conflict-safe")
	}

	end := time.Now().UTC().Add(30 * 24 * time.Hour)
	if err := st.UpsertSub(ctx, SubRow{AccountID: acct, OrderID: "ORD_1", PlanName: "月付基础", Status: "active", PeriodEnd: end}); err != nil {
		t.Fatal(err)
	}
	row, found, err := st.GetSub(ctx, acct)
	if err != nil || !found || row.Status != "active" || row.PlanName != "月付基础" {
		t.Fatalf("mirror %+v found=%v err=%v", row, found, err)
	}
	// COALESCE: 空 period_end 不擦旧值; 空 plan_name 不覆盖
	if err := st.UpsertSub(ctx, SubRow{AccountID: acct, OrderID: "ORD_1", Status: "canceling"}); err != nil {
		t.Fatal(err)
	}
	row, _, _ = st.GetSub(ctx, acct)
	if row.Status != "canceling" || row.PlanName != "月付基础" {
		t.Fatalf("upsert semantics broken: %+v", row)
	}
	if d := row.PeriodEnd.Sub(end); d < -time.Second || d > time.Second {
		t.Fatalf("period_end drifted: %+v end=%v", row, end)
	}
	// 清理测试行
	_, _ = pool.Exec(ctx, `DELETE FROM uc_subscriptions WHERE account_id=$1`, acct)
	_, _ = pool.Exec(ctx, `DELETE FROM uc_waffo_events WHERE event_id=$1`, "evt-"+acct)
}
