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
	"vpnmaster/subparse"
)

// Poller 周期拉取上游订阅并落库（work-3.2）。
// 启动清单由 LoadSources 从 env+DB 重建: UI 添加的上游完整 URL 持久在 DB,
// 重启不再丢源(旧语义"仅 env 存活"已确认为 bug, 见 db.Upstream.URL 注释)。
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

// Source 是一个上游订阅来源。Name 入库唯一键, URL 同步持久到 DB(db.Upstream.URL)。
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

// LoadSources 构造启动拉取清单(重启恢复的唯一真相源 = DB):
//   - env 源(SUB_UPSTREAM_*_URL)权威优先, 并回写 DB(支持只改 env 的 token 轮换);
//   - DB 里非 env 同名且存有完整 URL 的行(admin 页添加的)直接恢复拉取;
//   - 旧版只存了掩码的历史行(URL 为空)跳过——admin 页显示"未跟踪",
//     点一次"编辑"重填即永久恢复(URL 现在落库, 后续重启不再需要恢复)。
func LoadSources(ctx context.Context, store *db.Store, env []Source) ([]Source, error) {
	out := append([]Source{}, env...)
	seen := map[string]bool{}
	for _, s := range env {
		seen[s.Name] = true
		if _, err := store.AddUpstream(ctx, s.Name, s.URL); err != nil {
			return nil, err
		}
		if _, err := store.UpdateUpstreamURL(ctx, s.Name, s.URL); err != nil {
			return nil, err
		}
	}
	rows, err := store.ListUpstreams(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if seen[r.Name] || r.URL == "" {
			continue
		}
		seen[r.Name] = true
		out = append(out, Source{Name: r.Name, URL: r.URL})
	}
	return out, nil
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
// 注: pollOne 会把完整 URL 落库, 重启后 LoadSources 从 DB 恢复, 不再依赖本进程存活。
func (p *Poller) AddSource(s Source) {
	p.Sources = append(p.Sources, s)
	p.backoff[s.Name] = 0
}

// RemoveSource 删除来源(管理页"删除上游"): 从拉取清单摘除, 防止 pollOne 幂等登记把行复活。
func (p *Poller) RemoveSource(name string) {
	out := p.Sources[:0]
	for _, s := range p.Sources {
		if s.Name != name {
			out = append(out, s)
		}
	}
	p.Sources = out
	delete(p.backoff, name)
	delete(p.nextRun, name)
}

// HasSource 报告某来源是否在运行拉取清单内(供 admin 页标记"未跟踪"的死记录)。
func (p *Poller) HasSource(name string) bool {
	for _, s := range p.Sources {
		if s.Name == name {
			return true
		}
	}
	return false
}

// UpdateSource 编辑既有来源 URL(管理页"编辑上游"); 命中返回 true。
func (p *Poller) UpdateSource(name, url string) bool {
	for i := range p.Sources {
		if p.Sources[i].Name == name {
			p.Sources[i].URL = url
			p.backoff[name] = 0
			delete(p.nextRun, name) // 立即允许手动拉取
			return true
		}
	}
	return false
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

	nodes, perr := subparse.Parse(raw, s.Name)
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
