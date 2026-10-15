package sub

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"vpnmaster/ingest/internal/db"
	"vpnmaster/ingest/internal/domain"
)

// Poller 周期拉取上游订阅并落库（work-3.2）。
// 真实 URL 只存在于配置/env, DB 里仅存掩码（db.Upstream.URLMasked）。
// 失败策略: 429/5xx 指数退避 1m→2m→4m 封顶; 成功回写 last_ok 并重置退避。
// 拉取成功的一轮 = 幂等 UpsertNodes + MarkStale（连续 2 轮未出现即删, 见 store）。
type Poller struct {
	Store    *db.Store
	Sources  []Source // 来源清单(name+真实URL)
	Interval time.Duration
	HTTP     *http.Client // 默认 15s 超时

	// staleThreshold: 连续 N 轮未出现即删除节点（计划定 2）。
	StaleThreshold int

	log *log.Logger

	backoff map[string]int // 每来源当前退避级别
	nextRun map[string]time.Time
}

// Source 是一个上游订阅来源。Name 入库, URL 只在本进程内存中使用。
type Source struct {
	Name string
	URL  string
}

var defaultBackoff = []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute}

// NewPoller 构造; Interval<=0 时默认 60s, StaleThreshold<=0 时默认 2。
func NewPoller(store *db.Store, sources []Source, interval time.Duration) *Poller {
	if interval <= 0 {
		interval = time.Minute
	}
	return &Poller{
		Store:          store,
		Sources:        sources,
		Interval:       interval,
		StaleThreshold: 2,
		HTTP:           &http.Client{Timeout: 15 * time.Second},
		log:            log.New(log.Writer(), "sub: ", log.LstdFlags),
		backoff:        map[string]int{},
		nextRun:        map[string]time.Time{},
	}
}

// backoffDelay 纯函数, 便于单测: 第 n 次(0-based)连续失败后的等待时长, 封顶 4m。
func backoffDelay(consecFails int) time.Duration {
	i := consecFails - 1 // 第一次失败取第 0 档
	if i < 0 {
		i = 0
	}
	if i >= len(defaultBackoff) {
		i = len(defaultBackoff) - 1
	}
	return defaultBackoff[i]
}

// Run 阻塞循环直到 ctx 取消; 每个来源按自己的 nextRun 独立调度。
func (p *Poller) Run(ctx context.Context) error {
	tick := p.Interval / 2
	if tick > 5*time.Second {
		tick = 5 * time.Second
	}
	timer := time.NewTicker(tick)
	defer timer.Stop()
	p.pollDue(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-timer.C:
			p.pollDue(ctx, now)
		}
	}
}

// pollDue 处理所有到期来源。
func (p *Poller) pollDue(ctx context.Context, now time.Time) {
	for _, s := range p.Sources {
		if !p.due(s.Name, now) {
			continue
		}
		p.pollOne(ctx, s)
	}
}

func (p *Poller) due(name string, now time.Time) bool {
	t, ok := p.nextRun[name]
	return !ok || !t.After(now)
}

// PollOnce 对所有来源立即各拉一轮(管理页"手动拉取"入口, work-7 复用)。
func (p *Poller) PollOnce(ctx context.Context) []error {
	var errs []error
	for _, s := range p.Sources {
		if err := p.pollOne(ctx, s); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// PollOnceFor 只拉指定来源一轮(管理页按来源手动拉取)。
func (p *Poller) PollOnceFor(ctx context.Context, name string) []error {
	for _, s := range p.Sources {
		if s.Name == name {
			err := p.pollOne(ctx, s)
			if err != nil {
				return []error{err}
			}
			return nil
		}
	}
	return []error{fmt.Errorf("unknown source %q", name)}
}

// AddSource 运行时新增来源(管理页"添加上游"入口), 同步登记入库。
func (p *Poller) AddSource(s Source) {
	p.Sources = append(p.Sources, s)
	p.backoff[s.Name] = 0
}

// pollOne 拉一个来源: 成功 → upsert+markstale+last_ok; 429/5xx → 退避+last_err+事件。
func (p *Poller) pollOne(ctx context.Context, s Source) error {
	if _, err := p.Store.AddUpstream(ctx, s.Name, s.URL); err != nil { // 幂等登记(只存掩码)
		return err
	}
	now := time.Now()
	raw, status, err := p.fetch(ctx, s.URL)
	switch {
	case err != nil:
		// 超时/网络错误: 同样记错并进退避(视作一次失败)
		p.backoff[s.Name]++
		p.nextRun[s.Name] = now.Add(backoffDelay(p.backoff[s.Name]))
		p.log.Printf("%s: fetch error: %v (next in %s)", s.Name, err, p.nextRun[s.Name].Sub(now).Round(time.Second))
		return p.recordErr(ctx, s, fmt.Sprintf("fetch: %v", err))
	case status >= 400:
		p.backoff[s.Name]++
		p.nextRun[s.Name] = now.Add(backoffDelay(p.backoff[s.Name]))
		p.log.Printf("%s: HTTP %d (next in %s)", s.Name, status, p.nextRun[s.Name].Sub(now).Round(time.Second))
		return p.recordErr(ctx, s, fmt.Sprintf("http %d", status))
	default:
		p.backoff[s.Name] = 0
		p.nextRun[s.Name] = now.Add(p.Interval)
	}

	nodes, perr := Parse(raw, s.Name)
	if perr != nil {
		// 拉到内容但解析失败: 不当作节点消失, 只记错(退避一档, 保持旧池)
		p.backoff[s.Name]++
		p.nextRun[s.Name] = now.Add(backoffDelay(p.backoff[s.Name]))
		return p.recordErr(ctx, s, "parse: "+perr.Error())
	}
	var fps []string
	seen := map[string]bool{}
	for _, n := range nodes {
		fp := domain.NewFingerprint(n)
		fps = append(fps, fp)
		seen[fp] = true
	}
	if _, _, uerr := p.Store.UpsertNodes(ctx, s.Name, nodes); uerr != nil {
		return p.recordErr(ctx, s, "upsert: "+uerr.Error())
	}
	if del, serr := p.Store.MarkStale(ctx, s.Name, fps, p.StaleThreshold); serr != nil {
		return p.recordErr(ctx, s, "markstale: "+serr.Error())
	} else if len(del) > 0 {
		p.log.Printf("%s: pruned %d stale node(s)", s.Name, len(del))
	}
	if id, ok := p.upstreamID(ctx, s.Name); ok {
		_ = p.Store.MarkUpstreamOK(ctx, id)
	}
	p.log.Printf("%s: ok, %d node(s)", s.Name, len(nodes))
	return nil
}

func (p *Poller) recordErr(ctx context.Context, s Source, msg string) error {
	if id, ok := p.upstreamID(ctx, s.Name); ok {
		_ = p.Store.MarkUpstreamErr(ctx, id, msg)
		_ = p.Store.RecordEvent(ctx, db.Event{Kind: "pull_err", NodeFP: "", From: s.Name, To: "", Detail: msg})
	}
	return fmt.Errorf("upstream %s: %s", s.Name, msg)
}

// fetch GET 订阅内容, 返回 (body, statusCode, error)。非 2xx 时 body 可为空但 status 有效。
func (p *Poller) fetch(ctx context.Context, url string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10MB 封顶
	if err != nil {
		return "", resp.StatusCode, err
	}
	return string(body), resp.StatusCode, nil
}

// dueState 暴露退避/调度状态, 测试与诊断用。
func (p *Poller) dueState(name string) (backoffLevel int, next time.Time, ok bool) {
	b, hasB := p.backoff[name]
	t, hasT := p.nextRun[name]
	return b, t, hasB || hasT
}

func (p *Poller) upstreamID(ctx context.Context, name string) (int64, bool) {
	list, err := p.Store.ListUpstreams(ctx)
	if err != nil {
		return 0, false
	}
	for _, u := range list {
		if u.Name == name {
			return u.ID, true
		}
	}
	return 0, false
}

// ParseSourceList 解析 env 风格的来源清单 "name1=url1,name2=url2"。
func ParseSourceList(s string) ([]Source, error) {
	var out []Source
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, url, found := strings.Cut(part, "=")
		if !found || name == "" || url == "" {
			return nil, fmt.Errorf("sub: bad source entry %q (want name=url)", part)
		}
		out = append(out, Source{Name: name, URL: url})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("sub: no sources in list")
	}
	return out, nil
}
