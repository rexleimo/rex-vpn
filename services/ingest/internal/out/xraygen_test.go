package out

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"vpnmaster/ingest/internal/db"
)

// 固定 3 节点输入(指纹/地址/传输层参数确定, 保证 golden 稳定)。
func goldenRows() []db.NodeRow {
	return []db.NodeRow{
		{
			Fingerprint: "aaaabbbb00001111aaaabbbb00001111",
			Address:     "1.2.3.4", Port: 443, Protocol: "vless",
			StreamJSON: `{"network":"ws","user":"uuid-a","sni":"one.example.com","fp":"chrome","wsPath":"/a"}`,
			SNI:        "one.example.com", Status: "active",
		},
		{
			Fingerprint: "ccccdddd00001111ccccdddd00001111",
			Address:     "5.6.7.8", Port: 443, Protocol: "vless",
			StreamJSON: `{"network":"ws","user":"uuid-b","sni":"two.example.com","fp":"chrome","wsPath":"/b"}`,
			SNI:        "two.example.com", Status: "active",
		},
		{
			Fingerprint: "eeeeffff00001111eeeeffff00001111",
			Address:     "9.10.11.12", Port: 8443, Protocol: "vless",
			StreamJSON: `{"network":"ws","user":"uuid-c","sni":"three.example.com","fp":"chrome","wsPath":"/c"}`,
			SNI:        "three.example.com", Status: "active",
		},
	}
}

// 5.1 验收: 3 节点输入 → golden config.json(字节级快照)。
func TestGenerateGolden(t *testing.T) {
	data, err := Generate(goldenRows(), Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	got := string(data)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err == nil {
			_ = os.WriteFile(filepath.Join("testdata", "golden-3nodes.json"), data, 0o644)
			t.Logf("golden updated")
		}
	}
	want, err := os.ReadFile(filepath.Join("testdata", "golden-3nodes.json"))
	if err != nil {
		t.Fatalf("read golden: %v (run with UPDATE_GOLDEN=1 to create)", err)
	}
	if got != string(want) {
		t.Fatalf("config != golden:\n--- got ---\n%s\n--- want ---\n%s", got, string(want))
	}
}

// 结构断言: selector 入口(freedom+leastPing) + catch-all 路由指向它 + tag 规则(防 golden 漂移语义回归)。
func TestGenerateStructure(t *testing.T) {
	data, err := Generate(goldenRows(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	mustUnmarshal(t, data, &cfg)
	// 负载均衡必须是 routing.balancers + observatory(leastPing):
	// 旧的 freedom+selector 伪 outbound 实测静默直连(2026-10-17); 顶层 balancers 会被忽略报 `balancer not found`。
	routing := cfg["routing"].(map[string]any)
	bals, ok := routing["balancers"].([]any)
	if !ok || len(bals) != 1 {
		t.Fatalf("routing.balancers = %v, want 1 个均衡器", routing["balancers"])
	}
	b := bals[0].(map[string]any)
	if b["tag"] != "upstream_lb" || b["strategy"].(map[string]any)["type"] != "leastPing" {
		t.Fatalf("balancer = %v", b)
	}
	if len(b["selector"].([]any)) != 3 {
		t.Fatalf("balancer selector = %v", b["selector"])
	}
	if obs, ok := cfg["observatory"].(map[string]any); !ok || obs["probeUrl"] == "" {
		t.Fatalf("observatory 缺失(leastPing 依赖): %v", cfg["observatory"])
	}
	outs := cfg["outbounds"].([]any)
	rules := cfg["routing"].(map[string]any)["rules"].([]any)
	if len(rules) != 2 {
		t.Fatalf("routing rules = %v", rules)
	}
	for i, r := range rules {
		m := r.(map[string]any)
		// 引用均衡器必须用 balancerTag; outboundTag 会报 `non existing outTag`
		if m["balancerTag"] != "upstream_lb" {
			t.Fatalf("rule[%d] must use balancerTag: %v", i, m)
		}
		// xray 26.x: field 规则必须带有效匹配字段
		if _, ok := m["domain"]; !ok {
			if _, ok := m["ip"]; !ok {
				t.Fatalf("rule[%d] has no effective field (26.x rejects): %v", i, m)
			}
		}
	}
	if len(outs) != 3 {
		t.Fatalf("outbounds = %d, want 3 (纯 vless 节点, 无伪 outbound)", len(outs))
	}
	for i := 0; i < len(outs); i++ {
		m := outs[i].(map[string]any)
		if m["protocol"] != "vless" {
			t.Fatalf("outbound[%d] protocol = %v", i, m["protocol"])
		}
	}
}

// 5.3 护栏: 无 ACTIVE 节点 → Generate 返回 nil(不生成); Sync 不写文件(mtime/内容不变)。
func TestGuardNoActive(t *testing.T) {
	if data, err := Generate(nil, Options{}); err != nil || data != nil {
		t.Fatalf("Generate(nil) = %s, %v; want nil", data, err)
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"legacy":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(cfgPath)

	store := newStore(t)
	ctx := context.Background()
	syncer := NewSyncer(store, cfgPath, Options{})
	wrote, err := syncer.Sync(ctx)
	if err != nil || wrote {
		t.Fatalf("Sync with empty pool = wrote:%v err:%v; want no write", wrote, err)
	}
	after, _ := os.Stat(cfgPath)
	if before.ModTime() != after.ModTime() {
		t.Fatalf("config mtime changed despite empty pool")
	}
	content, _ := os.ReadFile(cfgPath)
	if string(content) != `{"legacy":true}` {
		t.Fatalf("config content changed: %s", content)
	}
}

// 5.2 触发链: 池变化才写; 同池不重写; 写入记 config_write 事件。
func TestSyncWritesOnlyOnPoolChange(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	// 造 2 个 active 节点
	rows := goldenRows()
	for i := range rows {
		_ = store.UpsertRow(ctx, &rows[i])
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	syncer := NewSyncer(store, cfgPath, Options{})

	w1, err := syncer.Sync(ctx)
	if err != nil || !w1 {
		t.Fatalf("first Sync = wrote:%v err:%v; want write", w1, err)
	}
	w2, _ := syncer.Sync(ctx) // 同池
	if w2 {
		t.Fatal("second Sync (same pool) should skip write")
	}
	// 摘除一个 → 池变 → 重写
	if _, err := store.Evict(ctx, rows[0].Fingerprint); err != nil {
		t.Fatal(err)
	}
	w3, err := syncer.Sync(ctx)
	if err != nil || !w3 {
		t.Fatalf("third Sync (pool changed) = wrote:%v err:%v; want write", w3, err)
	}
	evs, _ := store.ListEvents(ctx, 100)
	cfgWrites := 0
	for _, e := range evs {
		if e.Kind == "config_write" {
			cfgWrites++
		}
	}
	if cfgWrites != 2 {
		t.Fatalf("config_write events = %d, want 2", cfgWrites)
	}
}

func newStore(t *testing.T) *db.Store {
	t.Helper()
	s, err := db.NewStore("sqlite", filepath.Join(t.TempDir(), "out.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustUnmarshal(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}
