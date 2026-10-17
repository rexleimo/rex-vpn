package subparse

import (
	"encoding/base64"
	"errors"
	"strings"
)

// Parse 把订阅原始内容归一化为节点列表。
// 支持: 纯链接列表(vless/vmess/shadowsocks/trojan) 与 Clash YAML(proxies),
// 内容可直接是明文, 也可以是 base64 包裹(主流 sub 服务两种都见过)。
// sourceTag 记入每个节点, 用于跨上游去重溯源。
func Parse(raw, sourceTag string) ([]Node, error) {
	content := strings.TrimSpace(raw)
	if decoded, ok := tryBase64(content); ok {
		content = decoded
	}
	// Clash YAML 特征: proxies/proxy-groups 键; 纯链接特征: 行内出现 scheme
	if isClashYAML(content) {
		return parseClashYAML(content, sourceTag)
	}
	if !strings.Contains(content, "://") {
		return nil, errors.New("sub: unrecognized subscription content (neither link list nor clash yaml)")
	}
	return parseLinkList(content, sourceTag)
}

// tryBase64 宽松判定 base64 包裹: 去掉空白后为合法 base64 且解码后含 scheme/yaml 特征。
func tryBase64(content string) (string, bool) {
	compact := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, content)
	decoded, err := base64.StdEncoding.DecodeString(compact)
	if err != nil || len(decoded) == 0 {
		return "", false
	}
	text := string(decoded)
	if strings.Contains(text, "://") || isClashYAML(text) {
		return text, true
	}
	return "", false
}

// isClashYAML 特征匹配(yaml 头部或 proxies 键), 不做全量 yaml 解析判断。
func isClashYAML(s string) bool {
	return strings.HasPrefix(s, "proxies:") ||
		strings.HasPrefix(s, "proxy-groups:") ||
		(strings.Contains(s, "\nproxies:") && strings.Contains(s, " - name:"))
}
