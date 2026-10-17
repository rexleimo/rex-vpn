// Package domain 定义 ingest 的领域实体。
// 这是最内层包: 被 db/sub/probe 依赖, 自身零外部依赖(除 stdlib + 共享 subparse)。
// Node/Stream/Protocol 以 alias 直接采用共享解析包(subparse, ADR-0005):
// 订阅节点的"理解方式"只存在一份权威, ingest 侧追加存储字段语义与指纹/状态机。
package domain

import (
	"crypto/md5"
	"encoding/hex"
	"strconv"
	"strings"

	"vpnmaster/subparse"
)

// Protocol 是支持的代理协议族(权威定义 = subparse)。
type Protocol = subparse.Protocol

const (
	ProtoVLess  = subparse.ProtoVLess
	ProtoVMESS  = subparse.ProtoVMESS
	ProtoShadow = subparse.ProtoShadow
	ProtoTrojan = subparse.ProtoTrojan
)

// NodeStatus 是节点生命周期状态(转移逻辑归 probe 包, 状态值归本包)。
type NodeStatus string

const (
	NodePending NodeStatus = "pending" // 已入库, 未探测
	NodeActive  NodeStatus = "active"
	NodeEvicted NodeStatus = "evicted"
)

// Stream 描述传输层配置(network/ws path/tls/sni 等), 按协议归一化。
// 权威定义与 StreamJSON 生成逻辑 = subparse。
type Stream = subparse.Stream

// Node 是归一化后的单个上游节点(ID/Fingerprint 由存储层填充)。
// 权威定义 = subparse; 去重键 Fingerprint = md5(addr|port|protocol|streamJSON|sni)。
type Node = subparse.Node

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
