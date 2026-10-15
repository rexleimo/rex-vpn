package probe

import (
	"context"
	"testing"
	"time"

	"vpnmaster/ingest/internal/domain"
	"vpnmaster/ingest/internal/testutil"
)

// 全矩阵转移测试(4.2 验收)。
func TestStateMatrix(t *testing.T) {
	cases := []struct {
		cur            domain.NodeStatus
		ok             bool
		fails, succes  int
		wantNext       domain.NodeStatus
		wantMoved      bool
	}{
		{domain.NodeActive, true, 0, 1, domain.NodeActive, false},
		{domain.NodeActive, false, 1, 0, domain.NodeActive, false},
		{domain.NodeActive, false, 2, 0, domain.NodeActive, false},
		{domain.NodeActive, false, 3, 0, domain.NodeEvicted, true}, // 连续 3 败摘除
		{domain.NodeEvicted, true, 0, 1, domain.NodeEvicted, false},
		{domain.NodeEvicted, true, 0, 2, domain.NodeActive, true}, // 连续 2 成回池
		{domain.NodeEvicted, false, 1, 0, domain.NodeEvicted, false},
		{domain.NodePending, true, 0, 1, domain.NodeActive, true}, // 首探成功入池
		{domain.NodePending, false, 1, 0, domain.NodePending, false},
	}
	for i, c := range cases {
		got, moved := NextStatus(c.cur, c.ok, c.fails, c.succes)
		if got != c.wantNext || moved != c.wantMoved {
			t.Errorf("case%d (%s ok=%v f=%d s=%d) = (%s,%v), want (%s,%v)",
				i, c.cur, c.ok, c.fails, c.succes, got, moved, c.wantNext, c.wantMoved)
		}
	}
}

func TestAllEvicted(t *testing.T) {
	if AllEvicted(nil) {
		t.Fatal("empty should be false")
	}
	if !AllEvicted([]domain.NodeStatus{domain.NodeEvicted, domain.NodeEvicted}) {
		t.Fatal("all evicted should be true")
	}
	if AllEvicted([]domain.NodeStatus{domain.NodeActive, domain.NodeEvicted}) {
		t.Fatal("has active should be false")
	}
}

// Prober 驱动: 活节点入池并写回延迟(4.1 验收)。
func TestProberHealthyEntersPool(t *testing.T) {
	fn, err := testutil.NewFakeNode(t, "probe.test")
	if err != nil {
		t.Fatal(err)
	}
	store := newMemStore(t)
	ctx := context.Background()
	if _, _, err := store.UpsertNodes(ctx, "src", fakeNodeRows(fn, "n1")); err != nil {
		t.Fatal(err)
	}
	p := NewProber(store)
	p.Timeout = 2 * time.Second
	if _, err := p.ProbeRound(ctx); err != nil {
		t.Fatal(err)
	}
	acts, _ := store.ListNodes(ctx, string(domain.NodeActive))
	if len(acts) != 1 {
		t.Fatalf("active = %d, want 1 (pending→active on first good probe)", len(acts))
	}
}

// 连续 3 败摘除(4.2)。
func TestProberEvictsAfterThreeFails(t *testing.T) {
	// 指向一个从未监听的端口 → 每次探测都失败
	store := newMemStore(t)
	ctx := context.Background()
	if _, _, err := store.UpsertNodes(ctx, "src", deadNodeRows("127.0.0.1", 1)); err != nil {
		t.Fatal(err)
	}
	// 手工置 active(模拟已入池后开始故障)
	_ = store.SetNodeStatus(ctx, domain.NewFingerprint(deadNodeRows("127.0.0.1", 1)[0]), string(domain.NodePending), string(domain.NodeActive))
	p := NewProber(store)
	p.Timeout = 200 * time.Millisecond
	for i := 0; i < 3; i++ {
		_, _ = p.ProbeRound(ctx)
	}
	ev, _ := store.ListNodes(ctx, string(domain.NodeEvicted))
	if len(ev) != 1 {
		t.Fatalf("evicted = %d, want 1 after 3 fails", len(ev))
	}
}

// 事件落库(4.3)。
func TestProberRecordsEvents(t *testing.T) {
	fn, _ := testutil.NewFakeNode(t, "ev.test")
	store := newMemStore(t)
	ctx := context.Background()
	if _, _, err := store.UpsertNodes(ctx, "src", fakeNodeRows(fn, "n1")); err != nil {
		t.Fatal(err)
	}
	p := NewProber(store)
	p.Timeout = 2 * time.Second
	_, _ = p.ProbeRound(ctx) // pending→active (recover 事件)
	evs, _ := store.ListEvents(ctx, 100)
	if len(evs) == 0 {
		t.Fatal("no events after first probe transition")
	}
}

// 全灭保池(4.2): 全 evicted 时 LastHealthy 保留最后一次健康集, Degraded=true。
func TestProberKeepsLastHealthyWhenAllDown(t *testing.T) {
	fn, _ := testutil.NewFakeNode(t, "pool.test")
	store := newMemStore(t)
	ctx := context.Background()
	if _, _, err := store.UpsertNodes(ctx, "src", fakeNodeRows(fn, "n1")); err != nil {
		t.Fatal(err)
	}
	p := NewProber(store)
	p.Timeout = 2 * time.Second
	_, _ = p.ProbeRound(ctx) // n1 → active
	if got := p.LastHealthy(); len(got) != 1 {
		t.Fatalf("LastHealthy = %d, want 1", len(got))
	}
	// 关停节点 → 3 轮失败摘除 → 全灭
	fn.MarkDown()
	for i := 0; i < 3; i++ {
		_, _ = p.ProbeRound(ctx)
	}
	if !p.Degraded() {
		t.Fatal("Degraded should be true when all nodes evicted")
	}
	if got := p.LastHealthy(); len(got) != 1 {
		t.Fatalf("LastHealthy after all-down = %d, want 1 (keep last pool)", len(got))
	}
}
