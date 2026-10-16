package sub

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"vpnmaster/ingest/internal/domain"
)

// parseClashYAML 解析 Clash 订阅 yaml 的 proxies 段。
// 仅支持本项目协议白名单(vless/vmess/shadowsocks/trojan); 其他类型节点跳过并计数。
func parseClashYAML(content, sourceTag string) ([]domain.Node, error) {
	var doc struct {
		Proxies []clashProxy `yaml:"proxies"`
	}
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return nil, fmt.Errorf("clash yaml: %w", err)
	}
	var nodes []domain.Node
	skipped := 0
	for _, p := range doc.Proxies {
		n, ok := clashProxyToNode(p, sourceTag)
		if !ok {
			skipped++
			continue
		}
		nodes = append(nodes, n)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("clash yaml yielded 0 supported proxies (skipped %d)", skipped)
	}
	return nodes, nil
}

type clashProxy struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`
	Server   string `yaml:"server"`
	Port     int    `yaml:"port"`
	Password string `yaml:"password"`
	UDP      bool   `yaml:"udp"`
	// ss
	Method string `yaml:"method"`
	// vless/vmess
	UUID     string `yaml:"uuid"`
	Username string `yaml:"username"`
	// vless
	Flow        string `yaml:"flow"`
	Network     string `yaml:"network"`
	SkipCertChk bool   `yaml:"skip-cert-verify"`
	// tls
	Reality bool   `yaml:"reality"`
	SNI     string `yaml:"sni"`
	Finger  string `yaml:"fingerprint"`
	// ws
	WSOpts struct {
		Path string `yaml:"path"`
		Host string `yaml:"host"`
	} `yaml:"ws-opts"`
}

func clashProxyToNode(p clashProxy, sourceTag string) (domain.Node, bool) {
	switch domain.Protocol(p.Type) {
	case domain.ProtoVLess, domain.ProtoVMESS, domain.ProtoShadow, domain.ProtoTrojan:
	default:
		return domain.Node{}, false
	}
	net := p.Network
	if net == "" {
		net = "tcp"
	}
	sni := p.SNI
	if sni == "" {
		sni = p.WSOpts.Host
	}
	var user string
	switch p.Type {
	case "vless", "vmess":
		user = p.UUID // clash 里 vless/vmess 凭证在 uuid 字段
		if user == "" {
			user = p.Username
		}
	default: // shadowsocks / trojan
		user = p.Password
		if p.Type == "shadowsocks" && p.Method != "" {
			user = p.Method + ":" + p.Password
		}
	}
	node := domain.Node{
		Address:  p.Server,
		Port:     p.Port,
		Protocol: domain.Protocol(p.Type),
		Tag:      orDefault(p.Name, sourceTag),
		Stream: domain.Stream{
			User:    user,
			Network: net,
			SNI:     sni,
			Finger:  p.Finger,
			WSPath:  p.WSOpts.Path,
		},
	}
	node.Stream.StreamJSON = streamJSON(node)
	return node, true
}
