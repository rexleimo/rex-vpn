package subparse

// Protocol 是支持的代理协议族(白名单)。
type Protocol string

const (
	ProtoVLess  Protocol = "vless"
	ProtoVMESS  Protocol = "vmess"
	ProtoShadow Protocol = "shadowsocks"
	ProtoTrojan Protocol = "trojan"
)

// Stream 描述传输层配置(network/ws path/tls/sni 等), 按协议归一化。
// StreamJSON 保留归一化后的完整传输配置(含协议凭证), 供 xraygen/sing-box 配置生成直填 outbound。
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
// ID/Fingerprint 由消费方(ingest 存储/去重)填充, 解析层永远置零;
// 字段形态与 ingest 领域实体完全一致(domain 包以 alias 消费, 避免二次映射)。
type Node struct {
	ID          int64
	Fingerprint string
	Address     string
	Port        int
	Protocol    Protocol
	Stream      Stream
	Tag         string // 上游来源标签(如 real-sub-vpn4zx), 溯源用
}
