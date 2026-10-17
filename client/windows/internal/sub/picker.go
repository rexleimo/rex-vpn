package sub

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	"vpnmaster/subparse"
)

// 客户端只连 xray-user(Marzban 固定入口)下发的入口列表——
// 上游池的节点选择/摘除全在服务端 xray-edge 完成(docs/phase2-windows-client.md §2)。
// picker 对入口列表做并行连通性探测, 产出按 RTT 升序候选, 供 url_test 自动切换兜底。

// ProbeResult 单个入口的探测结果: RTT 越小越优, Err 非 nil = 不入候选。
type ProbeResult struct {
	Node subparse.Node
	RTT  time.Duration
	Err  error
}

// probeDialer 单次探测实现(生产 = TLS/TCP 连通探测, 测试注入假计时)。
type probeDialer func(ctx context.Context, n subparse.Node) (time.Duration, error)

// TopN 并行探测 nodes, 返回按 RTT 升序的前 topN 个可达候选。
// timeout 为单次探测上限; concurrency ≤0 取默认 8。
func TopN(ctx context.Context, nodes []subparse.Node, topN int, timeout time.Duration, concurrency int) []ProbeResult {
	return topNWith(ctx, nodes, topN, timeout, concurrencyOr(concurrency, 8), tlsProbe)
}

func topNWith(ctx context.Context, nodes []subparse.Node, topN int, timeout time.Duration, concurrency int, dial probeDialer) []ProbeResult {
	if topN <= 0 {
		topN = len(nodes)
	}
	results := make([]ProbeResult, len(nodes))
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	for i := range nodes {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			rtt, err := dial(cctx, nodes[idx])
			results[idx] = ProbeResult{Node: nodes[idx], RTT: rtt, Err: err}
		}(i)
	}
	wg.Wait()

	cands := make([]ProbeResult, 0, len(nodes))
	for _, r := range results {
		if r.Err == nil {
			cands = append(cands, r)
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].RTT < cands[j].RTT })
	if len(cands) > topN {
		cands = cands[:topN]
	}
	return cands
}

// Candidates 把探测结果拍到出口标签序列(url_test 组的 outbounds 引用顺序 = 优先级顺序)。
func Candidates(results []ProbeResult) []subparse.Node {
	out := make([]subparse.Node, len(results))
	for i, r := range results {
		out[i] = r.Node
	}
	return out
}

// tlsProbe 对入口做 TLS 握手计时(无 TLS 的 tcp 条目退化为 TCP 连接计时)。
func tlsProbe(ctx context.Context, n subparse.Node) (time.Duration, error) {
	start := time.Now()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addrPort(n))
	if err != nil {
		return time.Since(start), fmt.Errorf("probe dial %s: %w", addrPort(n), err)
	}
	defer func() { _ = conn.Close() }()

	if !streamTLS(n.Stream) {
		return time.Since(start), nil
	}
	host, _, _ := addrPortSplit(n)
	serverName := orDefault(n.Stream.SNI, host)
	tc := tls.Client(conn, &tls.Config{ServerName: serverName, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12})
	if err := tc.HandshakeContext(ctx); err != nil {
		return time.Since(start), fmt.Errorf("probe tls %s: %w", addrPort(n), err)
	}
	return time.Since(start), nil
}

// streamTLS 是否走 TLS: vless/vmess/trojan 条目 security=tls 归一化后以 Network 记录不了,
// 这里按 SNI/端口保守判断——有 SNI 或 fp 即视为 TLS 入口; vmess JSON 的 tls=1 已并入 SNI 逻辑。
// 注: 精确的 TLS 判定在 sing-box 配置侧由 outbounds.tls.enabled 表达, 探测只求保守覆盖。
func streamTLS(s subparse.Stream) bool { return s.SNI != "" || s.Finger != "" }

func addrPort(n subparse.Node) string {
	host, port, _ := addrPortSplit(n)
	return net.JoinHostPort(host, itoa(port))
}

func addrPortSplit(n subparse.Node) (host string, port int, ok bool) {
	if n.Address == "" || n.Port <= 0 {
		return "", 0, false
	}
	return n.Address, n.Port, true
}

func concurrencyOr(c, def int) int {
	if c <= 0 {
		return def
	}
	return c
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func itoa(v int) string {
	return fmt.Sprintf("%d", v)
}
