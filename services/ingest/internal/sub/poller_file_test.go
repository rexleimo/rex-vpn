package sub

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// file:// 源: 机场订阅 URL 被风控时, 从本地 clash yaml 建档(WestData 场景)。
func TestPollFileSource(t *testing.T) {
	store := newTestStore(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "westdata.yaml")
	content := `proxies:
  - {name: "HK 01", type: trojan, server: 1.2.3.4, port: 20001, password: pw1, sni: mpvideo.qpic.cn, skip-cert-verify: true, udp: true}
  - {name: "HK 02", type: trojan, server: 1.2.3.4, port: 20002, password: pw1, sni: mpvideo.qpic.cn, skip-cert-verify: true, udp: true}
proxy-groups: []
rules: []
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	p := NewPoller(store, []Source{{Name: "westdata", URL: "file://" + path}}, time.Minute)
	if errs := p.PollOnce(context.Background()); len(errs) != 0 {
		t.Fatalf("PollOnce: %v", errs)
	}
	rows, err := store.ListNodes(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("nodes = %d, want 2", len(rows))
	}
	for _, r := range rows {
		if r.Protocol != "trojan" || r.Source != "westdata" || r.Status != "pending" {
			t.Fatalf("bad row: %+v", r)
		}
	}
	// 文件被替换 → 下一轮幂等更新(stale 清理旧节点)
	if err := os.WriteFile(path, []byte(content[:len(content)-len("proxy-groups: []\nrules: []\n")]), 0o600); err != nil {
		t.Fatal(err)
	}
	if errs := p.PollOnce(context.Background()); len(errs) != 0 {
		t.Fatalf("PollOnce#2: %v", errs)
	}
	// file:// 读不到文件 → 报错进退避, 不清池
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if errs := p.PollOnce(context.Background()); len(errs) != 1 {
		t.Fatalf("missing file should yield 1 error, got %v", errs)
	}
}
