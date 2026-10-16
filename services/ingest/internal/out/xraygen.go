// Package out 负责 xray-edge 配置生成与热加载触发（work-5）。
//
// xraygen: inbound(ws :10086) + 每个 ACTIVE 节点一个 vless outbound(tag up_<fingerprint>)
// + 一个 freedom "selector" outbound(tag upstream_lb, leastPing) 聚合这些节点 + routing(catch-all → upstream_lb)。
// (xray 26.10.10 实测: 顶层 balancers + routing 指向 balancer tag 会 `non existing outTag`; 改用 selector outbound。)
// 原子写(.tmp+rename); 护栏: 无 ACTIVE 节点时不写新 config(保持旧池)。
// 触发链: 状态变化 → Sync(池变化才写) → edge 容器 inotify watcher 优雅重启。
package out

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"vpnmaster/ingest/internal/db"
)

// 默认值(与 deploy/edge/config-base.json 对齐)。
const (
	DefaultInboundPort     = 10086
	DefaultInboundWSPath   = "/edge-ws"
	DefaultInboundClientID = "00000000-0000-4000-8000-000000000001"
	DefaultBalancerTag     = "upstream_lb"
	DefaultAPIListen       = "127.0.0.1:8080"
)

// Options 覆盖 xray 生成的关键参数。
type Options struct {
	InboundPort     int    // 0 → DefaultInboundPort
	InboundWSPath   string // "" → DefaultInboundWSPath
	InboundClientID string // "" → DefaultInboundClientID
	AccessLogPath   string // "" → 不记访问日志; 设路径(如 /tmp/xray-access.log)后 load-50 可按 outbound tag 聚合分流分布
}

func (o Options) withDefaults() Options {
	if o.InboundPort == 0 {
		o.InboundPort = DefaultInboundPort
	}
	if o.InboundWSPath == "" {
		o.InboundWSPath = DefaultInboundWSPath
	}
	if o.InboundClientID == "" {
		o.InboundClientID = DefaultInboundClientID
	}
	return o
}

// ---- 生成结构(字段序固定, 保证 golden 快照稳定) ----

type apiSec struct {
	Listen   string   `json:"listen"`
	Tag      string   `json:"tag"`
	Services []string `json:"services"`
}
type logSec struct {
	Access   string `json:"access"`
	Error    string `json:"error"`
	Loglevel string `json:"loglevel"`
}
type inboundSettings struct {
	Clients    []clientRef `json:"clients"`
	Decryption string      `json:"decryption"`
}
type clientRef struct {
	ID   string `json:"id"`
	Flow string `json:"flow"`
}
type inboundStream struct {
	Network    string `json:"network"`
	WSSettings *ws    `json:"wsSettings,omitempty"`
}
type inbound struct {
	Tag            string          `json:"tag"`
	Listen         string          `json:"listen"`
	Port           int             `json:"port"`
	Protocol       string          `json:"protocol"`
	Settings       inboundSettings `json:"settings"`
	StreamSettings inboundStream   `json:"streamSettings"`
}
type ws struct {
	Path string `json:"path"`
}
type vnextUser struct {
	ID         string `json:"id"`
	Encryption string `json:"encryption"`
	Flow       string `json:"flow"`
}
type vnextEntry struct {
	Address string      `json:"address"`
	Port    int         `json:"port"`
	Users   []vnextUser `json:"users"`
}
type outboundSettings struct {
	Vnext []vnextEntry `json:"vnext"`
}
type outboundStream struct {
	Network    string  `json:"network"`
	Security   string  `json:"security,omitempty"`
	TLS        *tlsSec `json:"tlsSettings,omitempty"`
	WSSettings *ws     `json:"wsSettings,omitempty"`
}
type tlsSec struct {
	ServerName  string `json:"serverName,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}
type outbound struct {
	Tag            string           `json:"tag"`
	Protocol       string           `json:"protocol"`
	Settings       outboundSettings `json:"settings"`
	StreamSettings outboundStream   `json:"streamSettings"`
}

// selectorOutbound 是 xray 26.x 的负载均衡入口: 一个 freedom outbound,
// 通过 selector 聚合多个节点 tag, 由 balancingStrategy(leastPing) 分流。
// routing 的 outboundTag 指向本 tag(upstream_lb)。
type selectorOutbound struct {
	Protocol          string   `json:"protocol"`
	Tag               string   `json:"tag"`
	Selector          []string `json:"selector"`
	BalancingStrategy string   `json:"balancingStrategy"`
}
type routeRule struct {
	Type        string   `json:"type"`
	Domain      []string `json:"domain,omitempty"`
	IP          []string `json:"ip,omitempty"`
	OutboundTag string   `json:"outboundTag"`
}
type routingSec struct {
	DomainStrategy string      `json:"domainStrategy"`
	Rules          []routeRule `json:"rules"`
}
type xrayConfig struct {
	API       apiSec     `json:"api"`
	Log       logSec     `json:"log"`
	Inbounds  []inbound  `json:"inbounds"`
	Outbounds []any      `json:"outbounds"` // [selectorOutbound, outbound(vless)...]
	Routing   routingSec `json:"routing"`
	Stats     any        `json:"stats"`
}

// streamJSON 是 parser 归一化的传输层 JSON(见 sub.streamJSON)。
type streamJSON struct {
	Network string `json:"network"`
	User    string `json:"user"`
	SNI     string `json:"sni"`
	Finger  string `json:"fp"`
	WSPath  string `json:"wsPath"`
}

// Generate 从 ACTIVE 节点生成 xray-edge 完整配置(5.1)。
// 非 vless 节点跳过(日志可见), 0 个可用节点返回 (nil, nil) —— 调用方据此跳过写文件(护栏 5.3)。
func Generate(nodes []db.NodeRow, opt Options) ([]byte, error) {
	opt = opt.withDefaults()
	cfg := xrayConfig{
		API: apiSec{Listen: DefaultAPIListen, Tag: "api", Services: []string{"Handler", "Stats", "Routing"}},
		Log: logSec{Access: opt.AccessLogPath, Loglevel: "warning"},
		Inbounds: []inbound{{
			Tag:      "in_edge_ws",
			Listen:   "0.0.0.0",
			Port:     opt.InboundPort,
			Protocol: "vless",
			Settings: inboundSettings{
				Clients:    []clientRef{{ID: opt.InboundClientID, Flow: ""}},
				Decryption: "none",
			},
			StreamSettings: inboundStream{Network: "ws", WSSettings: &ws{Path: opt.InboundWSPath}},
		}},
		Outbounds: []any{}, // 首个为 selector(freedom) 入口, 其后是各节点 vless outbound; 池空时不写(护栏)
	}
	tags := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n.Protocol != "vless" {
			// MVP 上游池为 vless+ws+tls(实测); 其他协议暂不进池
			continue
		}
		var sj streamJSON
		if n.StreamJSON != "" {
			if err := json.Unmarshal([]byte(n.StreamJSON), &sj); err != nil {
				return nil, fmt.Errorf("node %s: bad stream json: %w", n.Fingerprint, err)
			}
		}
		netw := sj.Network
		if netw == "" {
			netw = "tcp"
		}
		tag := "up_" + n.Fingerprint
		tags = append(tags, tag)
		ob := outbound{
			Tag:      tag,
			Protocol: "vless",
			Settings: outboundSettings{Vnext: []vnextEntry{{
				Address: n.Address,
				Port:    n.Port,
				Users:   []vnextUser{{ID: sj.User, Encryption: "none", Flow: ""}},
			}}},
			StreamSettings: outboundStream{Network: netw},
		}
		if sj.SNI != "" {
			ob.StreamSettings.Security = "tls"
			ob.StreamSettings.TLS = &tlsSec{ServerName: sj.SNI, Fingerprint: sj.Finger}
		}
		if netw == "ws" {
			p := sj.WSPath
			if p == "" {
				p = "/"
			}
			ob.StreamSettings.WSSettings = &ws{Path: p}
		}
		cfg.Outbounds = append(cfg.Outbounds, ob)
	}
	if len(tags) == 0 {
		return nil, nil // 护栏: 无可用节点, 不生成(保持旧池)
	}
	// xray 26.x 负载均衡入口: freedom outbound 携带 selector(tag 列表) + leastPing。
	cfg.Outbounds = append([]any{selectorOutbound{
		Protocol:          "freedom",
		Tag:               DefaultBalancerTag,
		Selector:          tags,
		BalancingStrategy: "leastPing",
	}}, cfg.Outbounds...)
	// xray 26.x 拒绝无匹配字段的 field 规则("no effective fields"): catch-all 拆为两条带字段的规则
	// (域名 regexp:.* + IP 0.0.0.0/0), 均指向 selector tag, 双保险防 final 旁路。
	cfg.Routing = routingSec{
		DomainStrategy: "IPIfNonMatch",
		Rules: []routeRule{
			{Type: "field", Domain: []string{"regexp:.*"}, OutboundTag: DefaultBalancerTag},
			{Type: "field", IP: []string{"0.0.0.0/0"}, OutboundTag: DefaultBalancerTag},
		},
	}
	cfg.Stats = map[string]any{}
	return json.MarshalIndent(cfg, "", "  ")
}

// AtomicWrite .tmp+rename 原子写(5.1); 写失败不影响旧文件。
func AtomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".xray-cfg-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功时 remove 报 err 无所谓
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Syncer 负责"池变化才写"的触发链(5.2/5.3): 池指纹集不变则跳过(避免 watcher 无谓重启),
// 全灭不写(护栏), 每次实际写入记 config_write 事件。
type Syncer struct {
	Store *db.Store
	Path  string
	Opts  Options
	last  string // 上一次写入的池指纹签名
}

func NewSyncer(store *db.Store, path string, opts Options) *Syncer {
	return &Syncer{Store: store, Path: path, Opts: opts}
}

// Sync 一轮: 返回 (是否实际写文件, 错误)。
func (s *Syncer) Sync(ctx context.Context) (bool, error) {
	rows, err := s.Store.ListNodes(ctx, "active")
	if err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return false, nil // 护栏(5.3): 无 ACTIVE 不写, 保持旧池
	}
	data, err := Generate(rows, s.Opts)
	if err != nil {
		return false, err
	}
	if data == nil {
		return false, nil
	}
	sig := poolSignature(rows)
	if sig == s.last {
		return false, nil // 池未变, 不触发重启
	}
	if err := AtomicWrite(s.Path, data); err != nil {
		return false, err
	}
	s.last = sig
	_ = s.Store.RecordEvent(ctx, db.Event{Kind: "config_write", From: "", To: sig})
	return true, nil
}

// poolSignature ACTIVE 池的确定性签名(排序指纹拼接), 用于"变化才写"判定。
func poolSignature(rows []db.NodeRow) string {
	sigs := make([]string, 0, len(rows))
	for _, r := range rows {
		sigs = append(sigs, r.Fingerprint)
	}
	sort.Strings(sigs)
	return strings.Join(sigs, "|")
}
