package probe

import (
	"path/filepath"
	"testing"

	"vpnmaster/ingest/internal/db"
	"vpnmaster/ingest/internal/domain"
	"vpnmaster/ingest/internal/testutil"
)

func newMemStore(t *testing.T) *db.Store {
	t.Helper()
	s, err := db.NewStore("sqlite", filepath.Join(t.TempDir(), "probe.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// fakeNodeRows 依据活假节点构造一条 domain.Node(供 upsert 入库)。
func fakeNodeRows(fn *testutil.FakeNode, tag string) []domain.Node {
	return []domain.Node{{
		Address: "127.0.0.1", Port: fn.Port, Protocol: domain.ProtoVLess, Tag: tag,
		Stream: domain.Stream{User: "uuid-" + tag, Network: "tcp", SNI: "probe.test",
			StreamJSON: `{"network":"tcp","user":"uuid-x","sni":"probe.test"}`},
	}}
}

// deadNodeRows 指向 127.0.0.1:1(无监听) 的死节点。
func deadNodeRows(addr string, port int) []domain.Node {
	return []domain.Node{{
		Address: addr, Port: port, Protocol: domain.ProtoVLess, Tag: "dead",
		Stream: domain.Stream{User: "uuid-dead", Network: "tcp", SNI: addr,
			StreamJSON: `{"network":"tcp","user":"uuid-dead"}`},
	}}
}
