package sub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vpnmaster/ingest/internal/db"
	"vpnmaster/ingest/internal/domain"
)

// newTestStore 用 temp-file sqlite, 每测试独立。
func newTestStore(t *testing.T) *db.Store {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.db")
	s, err := db.NewStore("sqlite", dsn)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustParse(t *testing.T, raw, tag string) []domain.Node {
	t.Helper()
	nodes, err := Parse(raw, tag)
	if err != nil {
		t.Fatalf("Parse(%q): %v", raw, err)
	}
	return nodes
}

const oneLineSub = "vless://fake-uuid-0001@10.0.0.1:443?security=tls&type=ws&host=example.test&fp=chrome&sni=example.test&path=%2F&encryption=none#NodeA\n"

// 成功路径: 节点 upsert, last_ok 回写, 退避清零。
func TestPollerSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(oneLineSub))
	}))
	defer srv.Close()

	store := newTestStore(t)
	p := NewPoller(store, []Source{{Name: "s1", URL: srv.URL}}, time.Minute)
	if errs := p.PollOnce(context.Background()); len(errs) != 0 {
		t.Fatalf("PollOnce errs = %v, want none", errs)
	}
	ups, _ := store.ListUpstreams(context.Background())
	if len(ups) != 1 || ups[0].LastOKAt.IsZero() {
		t.Fatalf("upstream not marked ok: %+v", ups)
	}
	nodes, _ := store.ListNodes(context.Background(), "")
	if len(nodes) != 1 || nodes[0].Address != "10.0.0.1" || nodes[0].Port != 443 {
		t.Fatalf("nodes = %+v", nodes)
	}
	if b, _, _ := p.dueState("s1"); b != 0 {
		t.Fatalf("backoff after success = %d, want 0", b)
	}
}

// 429 路径: 指数退避 1m→2m→4m 封顶, last_err 与 pull_err 事件落库。
func TestPoller429Backoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	store := newTestStore(t)
	p := NewPoller(store, []Source{{Name: "s1", URL: srv.URL}}, time.Minute)
	ctx := context.Background()

	for i, want := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 4 * time.Minute} {
		if errs := p.PollOnce(ctx); len(errs) != 1 {
			t.Fatalf("round %d: errs = %v, want 1", i, errs)
		}
		_, next, _ := p.dueState("s1")
		if d := next.Sub(time.Now()).Round(time.Second); d != want {
			t.Fatalf("round %d: next in %v, want %v", i, d, want)
		}
	}
	ups, _ := store.ListUpstreams(ctx)
	if len(ups) != 1 || ups[0].LastErr == "" {
		t.Fatalf("LastErr not recorded: %+v", ups)
	}
	evs, _ := store.ListEvents(ctx, 100)
	if len(evs) != 4 || evs[0].Kind != "pull_err" {
		t.Fatalf("events = %d (last kind %q), want 4x pull_err", len(evs), evs[0].Kind)
	}
}

// 超时路径: 网络错误同样记错并进退避。
func TestPollerTimeout(t *testing.T) {
	block := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer block.Close()

	store := newTestStore(t)
	p := NewPoller(store, []Source{{Name: "s1", URL: block.URL}}, time.Minute)
	p.HTTP.Timeout = 50 * time.Millisecond

	errs := p.PollOnce(context.Background())
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "fetch") {
		t.Fatalf("errs = %v, want 1 fetch/timeout error", errs)
	}
	ups, _ := store.ListUpstreams(context.Background())
	if len(ups) != 1 || ups[0].LastErr == "" {
		t.Fatalf("LastErr not recorded on timeout: %+v", ups)
	}
}

// 解析失败不当作节点消失: 旧池保留, 只记错。
func TestPollerParseFailureKeepsPool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 合法链接行后面跟垃圾行 —— 注意: parseLinkList 跳过坏行, 只要有合法行就成功,
		// 所以这里用"无 scheme 的纯垃圾"触发 0 节点解析失败。
		_, _ = w.Write([]byte("totally-garbage no scheme here\n"))
	}))
	defer srv.Close()

	store := newTestStore(t)
	p := NewPoller(store, []Source{{Name: "s1", URL: srv.URL}}, time.Minute)
	if _, _, err := store.UpsertNodes(context.Background(), "s1", mustParse(t, oneLineSub, "s1")); err != nil {
		t.Fatal(err)
	}
	if errs := p.PollOnce(context.Background()); len(errs) != 1 {
		t.Fatalf("errs = %v, want 1 parse failure", errs)
	}
	nodes, _ := store.ListNodes(context.Background(), "")
	if len(nodes) != 1 {
		t.Fatalf("pool drained on parse failure: %d node(s), want 1", len(nodes))
	}
}

// backoffDelay 档位与封顶断言。
func TestBackoffDelayCaps(t *testing.T) {
	for i, want := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 4 * time.Minute, 4 * time.Minute} {
		if got := backoffDelay(i + 1); got != want {
			t.Errorf("backoffDelay(%d) = %v, want %v", i+1, got, want)
		}
	}
	if got := backoffDelay(0); got != time.Minute {
		t.Errorf("backoffDelay(0) = %v, want %v (default to 1st tier)", got, time.Minute)
	}
}

// ParseSourceList env 清单解析。
func TestParseSourceList(t *testing.T) {
	got, err := ParseSourceList(" a=http://x/s?a=1 , b=https://y/sub ")
	if err != nil || len(got) != 2 || got[0].Name != "a" || got[1].URL != "https://y/sub" {
		t.Fatalf("ParseSourceList = %+v, %v", got, err)
	}
	if _, err := ParseSourceList("noequals"); err == nil {
		t.Fatal("want error for missing '='")
	}
	if _, err := ParseSourceList(""); err == nil {
		t.Fatal("want error for empty list")
	}
}
