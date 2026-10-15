package probe

import (
	"context"
	"crypto/tls"
	"log"
	"net"
	"strconv"
	"time"

	"vpnmaster/ingest/internal/db"
	"vpnmaster/ingest/internal/domain"
)

// Prober (4.1): 对节点做 TLS 握手探测, 测 RTT 写回 nodes.latency_ms,
// 并按状态机(4.2)驱动 active↔evicted 转移, 每次转移落事件(4.3)。
// 并发: 每轮最多 Workers 个节点同时探; 仅探 active/pending(首探)/evicted(重试周期内)。
type Prober struct {
	Store       *db.Store
	Timeout     time.Duration // 单节点探测超时, 默认 3s
	Workers     int           // 并发度, 默认 16
	RetryPeriod time.Duration // evicted 节点重试周期, 默认 2m

	log *log.Logger

	// 内存连续计数(重启清零, MVP 可接受)
	fails   map[string]int // fingerprint -> 连续失败
	succes  map[string]int // fingerprint -> 连续成功(用于回池)
	retryAt map[string]time.Time

	// 全灭保池: 记录最后一次非空的出池指纹集, 供 xraygen 全灭兜底
	lastHealthy []string
	degraded    bool
}

func NewProber(store *db.Store) *Prober {
	return &Prober{
		Store:       store,
		Timeout:     3 * time.Second,
		Workers:     16,
		RetryPeriod: 2 * time.Minute,
		log:         log.New(log.Writer(), "probe: ", log.LstdFlags),
		fails:       map[string]int{},
		succes:      map[string]int{},
		retryAt:     map[string]time.Time{},
	}
}

// ProbeRound 探一轮, 写回延迟并按状态机转移。返回本轮发生的转移数(供 xraygen 触发)。
func (p *Prober) ProbeRound(ctx context.Context) (int, error) {
	rows, err := p.Store.ListNodes(ctx, "")
	if err != nil {
		return 0, err
	}
	statuses := make([]domain.NodeStatus, 0, len(rows))
	var targets []db.NodeRow
	now := time.Now()
	for i := range rows {
		st := domain.NodeStatus(rows[i].Status)
		statuses = append(statuses, st)
		switch st {
		case domain.NodeActive, domain.NodePending:
			targets = append(targets, rows[i])
		case domain.NodeEvicted:
			if t, ok := p.retryAt[rows[i].Fingerprint]; ok && now.Before(t) {
				continue // 未到重试周期
			}
			targets = append(targets, rows[i])
		}
	}

	results := p.probeAll(ctx, targets)

	transitions := 0
	for i := range rows {
		row := &rows[i]
		fp := row.Fingerprint
		res, probed := results[fp]
		if !probed {
			continue // 未到重试周期, 状态不变
		}
		cur := domain.NodeStatus(row.Status)
		if res.OK {
			p.succes[fp]++
			p.fails[fp] = 0
		} else {
			p.fails[fp]++
			p.succes[fp] = 0
		}
		next, moved := NextStatus(cur, res.OK, p.fails[fp], p.succes[fp])
		if moved {
			if err := p.Store.SetNodeStatus(ctx, fp, string(cur), string(next)); err != nil {
				p.log.Printf("status %s %s→%s: %v", fp, cur, next, err)
			}
			transitions++
		}
		if res.OK {
			_ = p.Store.SetLatency(ctx, fp, res.RTTMS)
		}
		switch next {
		case domain.NodeEvicted:
			p.retryAt[fp] = now.Add(p.RetryPeriod)
			if moved {
				p.log.Printf("EVICT %s (consec fails=%d)", fp, p.fails[fp])
			}
		case domain.NodeActive:
			delete(p.retryAt, fp)
		}
	}

	// 全灭判定: 刷新 lastHealthy 与 degraded
	activeFps := p.activeFingerprints(ctx)
	if len(activeFps) > 0 {
		p.lastHealthy = activeFps
		p.degraded = false
	} else if len(statuses) > 0 {
		p.degraded = true
		p.log.Printf("ALERT: all nodes down, keeping last healthy pool (%d)", len(p.lastHealthy))
	}
	return transitions, nil
}

// LastHealthy 全灭兜底时保留的最后可用出池指纹集(供 xraygen)。
func (p *Prober) LastHealthy() []string { return p.lastHealthy }

// Degraded 全灭标志(供 /healthz)。
func (p *Prober) Degraded() bool { return p.degraded }

// probeAll 并发探测, 返回 fingerprint -> 结果。
func (p *Prober) probeAll(ctx context.Context, targets []db.NodeRow) map[string]ProbeOutcome {
	results := make(map[string]ProbeOutcome, len(targets))
	if len(targets) == 0 {
		return results
	}
	out := make(chan result, len(targets))
	workers := p.Workers
	if workers > len(targets) {
		workers = len(targets)
	}
	sem := make(chan struct{}, workers)
	for _, r := range targets {
		sem <- struct{}{}
		go func(r db.NodeRow) {
			defer func() { <-sem }()
			out <- result{fp: r.Fingerprint, o: p.probeOne(ctx, r)}
		}(r)
	}
	for range targets {
		r := <-out
		results[r.fp] = r.o
	}
	return results
}

// probeOne 单节点 TLS 握手探测, 实测握手 RTT。
func (p *Prober) probeOne(ctx context.Context, r db.NodeRow) ProbeOutcome {
	addr := net.JoinHostPort(r.Address, strconv.Itoa(r.Port))
	d := &net.Dialer{Timeout: p.Timeout}
	sni := r.SNI
	if sni == "" {
		sni = r.Address
	}
	conf := &tls.Config{ServerName: sni, InsecureSkipVerify: true} // 探测只关心握手成功, 不校验证书
	start := time.Now()
	conn, err := tls.DialWithDialer(d, "tcp", addr, conf)
	if err != nil {
		return ProbeOutcome{OK: false}
	}
	rtt := int(time.Since(start).Milliseconds())
	_ = conn.Close()
	return ProbeOutcome{OK: true, RTTMS: rtt}
}

func (p *Prober) activeFingerprints(ctx context.Context) []string {
	rows, err := p.Store.ListNodes(ctx, string(domain.NodeActive))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Fingerprint)
	}
	return out
}

type result struct {
	fp string
	o  ProbeOutcome
}
