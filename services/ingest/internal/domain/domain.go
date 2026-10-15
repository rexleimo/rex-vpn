// Package domain 定义 ingest 的领域实体。
// 这是最内层包: 被 db/sub/probe 依赖, 自身零外部依赖(除 stdlib)。
package domain

import (
	"crypto/md5"
	"encoding/hex"
	"strconv"
	"strings"
)

// Protocol 是支持的代理协议族。
type Protocol string

const (
	ProtoVLess  Protocol = "vless"
	ProtoVMESS  Protocol = "vmess"
	ProtoShadow Protocol = "shadowsocks"
	ProtoTrojan Protocol = "trojan"
)

// NodeStatus 是节点生命周期状态(转移逻辑归 probe 包, 状态值归本包)。
type NodeStatus string

const (
	NodePending NodeStatus = "pending" // 已入库, 未探测
	NodeActive  NodeStatus = "active"
	NodeEvicted NodeStatus = "evicted"
)

// Stream 描述传输层配置(network/ws path/tls/sni 等), 按协议归一化。
// StreamJSON 保留归一化后的完整传输配置(含用户凭证), 供 xraygen(work-5) 直填 outbound。
type Stream struct {
	Network    string `json:"network"`
	User       string `json:"user,omitempty"` // 协议凭证: vless uuid / vmess id / ss password / trojan password
	WSPath     string `json:"wsPath,omitempty"`
	SNI        string `json:"sni,omitempty"`
	Alpn       string `json:"alpn,omitempty"` // 逗号分隔
	Finger     string `json:"fp,omitempty"`
	StreamJSON string `json:"streamJson"`
}

// Node 是归一化后的单个上游节点。
// 去重键 Fingerprint = md5(addr|port|protocol|streamJSON|sni), 跨上游同节点自动合并。
type Node struct {
	ID          int64
	Fingerprint string
	Address     string
	Port        int
	Protocol    Protocol
	Stream      Stream
	Tag         string // 上游来源标签(如 real-sub-vpn4zx), 溯源用
}

// NewFingerprint 计算节点去重键。
func NewFingerprint(n Node) string {
	raw := strings.Join([]string{n.Address, strconv.Itoa(n.Port), string(n.Protocol), n.Stream.StreamJSON, n.Stream.SNI}, "|")
	sum := md5.Sum([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// FingerprintRaw 返回去重键原料(测试与日志用)。
func FingerprintRaw(n Node) string {
	return strings.Join([]string{n.Address, strconv.Itoa(n.Port), string(n.Protocol), n.Stream.StreamJSON, n.Stream.SNI}, "|")
}
