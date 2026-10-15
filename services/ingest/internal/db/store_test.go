package db

import (
	"context"
	"testing"
	"time"

	"vpnmaster/ingest/internal/domain"
)

// 验收: 建库/迁移幂等(3.1); 接口层单测跑在 in-memory sqlite, 零驱动泄漏(3.4)。
// shared-cache in-memory: 同 DSN 多连接共享一个内存库。

func newMemStore(t *testing.T, name string) *Store {
	t.Helper()
	s, err := NewStore("sqlite", "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("NewStore(in-memory %s): %v", name, err)
	}
	return s
}

func sampleNodes(source string) []domain.Node {
	return []domain.Node{
		{
			Address: "a.example.com", Port: 443, Protocol: domain.ProtoVLess, Tag: "A",
			Stream: domain.Stream{User: "uuid-a", Network: "ws", SNI: "a.example.com", WSPath: "/", StreamJSON: `{"network":"ws","user":"uuid-a","sni":"a.example.com"}`},
		},
		{
			Address: "b.example.com", Port: 443, Protocol: domain.ProtoVLess, Tag: "B",
			Stream: domain.Stream{User: "uuid-b", Network: "ws", SNI: "b.example.com", WSPath: "/", StreamJSON: `{"network":"ws","user":"uuid-b","sni":"b.example.com"}`},
		},
	}
}

// 迁移幂等: 同一 DSN 连续两次 AutoMigrate 不报错不破坏数据。
func TestMigrationIdempotent(t *testing.T) {
	s := newMemStore(t, "mig1")
	ctx := context.Background()
	if _, _, err := s.UpsertNodes(ctx, "src", sampleNodes("src")); err != nil {
		t.Fatal(err)
	}
	// 第二次开库(同 DSN 共享内存库)
	s2 := newMemStore(t, "mig1")
	nodes, err := s2.ListNodes(ctx, "")
	if err != nil || len(nodes) != 2 {
		t.Fatalf("second open: %d nodes, err=%v", len(nodes), err)
	}
}

// 幂等 upsert: 连续 3 轮拉取, 行数稳定不增长(3.4 验收)。
func TestUpsertIdempotent(t *testing.T) {
	s := newMemStore(t, "up1")
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		added, existed, err := s.UpsertNodes(ctx, "src", sampleNodes("src"))
		if err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		if i == 0 {
			if len(added) != 2 || len(existed) != 0 {
				t.Fatalf("round0 added=%d existed=%d, want 2/0", len(added), len(existed))
			}
		}
		if i > 0 && (len(added) != 0 || len(existed) != 2) {
			t.Fatalf("round%d added=%d existed=%d, want 0/2", i, len(added), len(existed))
		}
		nodes, _ := s.ListNodes(ctx, "")
		if len(nodes) != 2 {
			t.Fatalf("round%d: %d nodes, want 2 stable", i, len(nodes))
		}
	}
}

// 消失节点删除路径: 连续 2 轮未出现 → 删除并记 stale 事件(3.4 验收)。
func TestMarkStaleDeletion(t *testing.T) {
	s := newMemStore(t, "st1")
	ctx := context.Background()
	full := sampleNodes("src")
	if _, _, err := s.UpsertNodes(ctx, "src", full); err != nil {
		t.Fatal(err)
	}
	// 第 1 轮只出现节点 A: B stale_count=1, 不删
	onlyA := full[:1]
	if del, err := s.MarkStale(ctx, "src", fpOf(t, s, onlyA), 2); err != nil || len(del) != 0 {
		t.Fatalf("round1 deleted=%v err=%v, want none", del, err)
	}
	nodes, _ := s.ListNodes(ctx, "")
	if len(nodes) != 2 {
		t.Fatalf("after round1: %d nodes, want 2", len(nodes))
	}
	// 第 2 轮仍只有 A: B stale_count=2 达阈值 → 删除 + 事件
	if del, err := s.MarkStale(ctx, "src", fpOf(t, s, onlyA), 2); err != nil || len(del) != 1 {
		t.Fatalf("round2 deleted=%v err=%v, want 1", del, err)
	}
	nodes, _ = s.ListNodes(ctx, "")
	if len(nodes) != 1 || nodes[0].Address != "a.example.com" {
		t.Fatalf("after round2: %+v, want only A", nodes)
	}
	evs, _ := s.ListEvents(ctx, 10)
	found := false
	for _, e := range evs {
		if e.Kind == "stale" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no stale event recorded: %+v", evs)
	}
}

// 重新出现节点重置 stale_count。
func TestStaleResetOnRediscover(t *testing.T) {
	s := newMemStore(t, "st2")
	ctx := context.Background()
	full := sampleNodes("src")
	if _, _, err := s.UpsertNodes(ctx, "src", full); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkStale(ctx, "src", fpOf(t, s, full[:1]), 2); err != nil { // B → stale 1
		t.Fatal(err)
	}
	if _, _, err := s.UpsertNodes(ctx, "src", full); err != nil { // B 重现, stale 归零
		t.Fatal(err)
	}
	if del, err := s.MarkStale(ctx, "src", fpOf(t, s, full), 2); err != nil || len(del) != 0 {
		t.Fatalf("deleted=%v err=%v, want none after rediscovery", del, err)
	}
}

// fpOf 读取当前库中某来源的指纹列表。
func fpOf(t *testing.T, s *Store, nodes []domain.Node) []string {
	t.Helper()
	var out []string
	for _, n := range nodes {
		out = append(out, domain.NewFingerprint(n))
	}
	return out
}

func TestUpstreamCRUD(t *testing.T) {
	s := newMemStore(t, "up2")
	ctx := context.Background()
	created, err := s.AddUpstream(ctx, "vpn4zx", "https://sub.example.com/v?token=abcdef1234567890")
	if err != nil || !created {
		t.Fatalf("AddUpstream = %v, %v", created, err)
	}
	created, _ = s.AddUpstream(ctx, "vpn4zx", "https://x/y")
	if created {
		t.Fatal("duplicate AddUpstream should return created=false")
	}
	ups, _ := s.ListUpstreams(ctx)
	if len(ups) != 1 {
		t.Fatalf("upstreams = %d, want 1", len(ups))
	}
	if got := ups[0].URLMasked; got == "https://sub.example.com/v?token=abcdef1234567890" {
		t.Fatal("URL not masked")
	}
	if err := s.MarkUpstreamErr(ctx, ups[0].ID, "http 429"); err != nil {
		t.Fatal(err)
	}
	ups, _ = s.ListUpstreams(ctx)
	if ups[0].LastErr != "http 429" {
		t.Fatalf("LastErr = %q", ups[0].LastErr)
	}
	if err := s.MarkUpstreamOK(ctx, ups[0].ID); err != nil {
		t.Fatal(err)
	}
	ups, _ = s.ListUpstreams(ctx)
	if ups[0].LastOKAt.IsZero() || ups[0].LastErr != "" {
		t.Fatalf("after OK: %+v", ups[0])
	}
	_ = time.Now()
}

// Evict/Recover 手动操作接口(4.4): 条件转移 + 事件。
func TestEvictRecover(t *testing.T) {
	s := newMemStore(t, "er1")
	ctx := context.Background()
	if _, _, err := s.UpsertNodes(ctx, "src", sampleNodes("src")); err != nil {
		t.Fatal(err)
	}
	fp := domain.NewFingerprint(sampleNodes("src")[0])
	// 先入池
	if err := s.SetNodeStatus(ctx, fp, string(domain.NodePending), string(domain.NodeActive)); err != nil {
		t.Fatal(err)
	}
	// 重复 evict 不重复转移(已是 evicted 时 from=active 不命中)
	ok, err := s.Evict(ctx, fp)
	if err != nil || !ok {
		t.Fatalf("Evict = %v, %v; want true", ok, err)
	}
	nodes, _ := s.ListNodes(ctx, string(domain.NodeEvicted))
	if len(nodes) != 1 {
		t.Fatalf("evicted = %d, want 1", len(nodes))
	}
	if ok, _ := s.Evict(ctx, fp); ok {
		t.Fatal("second Evict on evicted node should be no-op")
	}
	if ok, err := s.Recover(ctx, fp); err != nil || !ok {
		t.Fatalf("Recover = %v, %v; want true", ok, err)
	}
	acts, _ := s.ListNodes(ctx, string(domain.NodeActive))
	if len(acts) != 1 {
		t.Fatalf("active after recover = %d, want 1", len(acts))
	}
	evts, _ := s.ListEvents(ctx, 100)
	kinds := map[string]int{}
	for _, e := range evts {
		kinds[e.Kind]++
	}
	if kinds["evict"] < 1 || kinds["recover"] < 2 { // 1 SetNodeStatus + 1 Evict; 1 SetNodeStatus + 1 Recover
		t.Fatalf("event kinds = %v", kinds)
	}
}

func TestNodeStatusAndEvents(t *testing.T) {
	s := newMemStore(t, "ns1")
	ctx := context.Background()
	if _, _, err := s.UpsertNodes(ctx, "src", sampleNodes("src")); err != nil {
		t.Fatal(err)
	}
	fps, _ := s.fpAll(ctx)
	if err := s.SetNodeStatus(ctx, fps[0], string(domain.NodeActive), string(domain.NodeEvicted)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLatency(ctx, fps[0], 42); err != nil {
		t.Fatal(err)
	}
	acts, _ := s.ListNodes(ctx, string(domain.NodeEvicted))
	if len(acts) != 1 {
		t.Fatalf("evicted = %d, want 1", len(acts))
	}
	evs, _ := s.ListEvents(ctx, 10)
	if len(evs) != 1 || evs[0].Kind != "evict" {
		t.Fatalf("events = %+v, want 1 evict", evs)
	}
}

// 辅助: 列出某来源全部指纹。
func (s *Store) fpAll(ctx context.Context) ([]string, error) {
	rows, err := s.ListNodes(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Fingerprint)
	}
	return out, nil
}
