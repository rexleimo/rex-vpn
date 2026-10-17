// Package route 定义客户端的默认路由策略(docs/client-impl.md §3):
//   - LAN/内网 → direct;
//   - 面板与订阅域名 → direct(防"刷新订阅自己套自己"的自我嵌套);
//   - 其余全部 → 代理, DNS 强制走代理防泄漏。
//
// 本包只表达"策略意图"(哪些域名直连/哪些走代理), 不认识 sing-box 的
// 具体 JSON 形态——映射归 kernel/cfggen, 策略与生成物可独立演化。
package route

import (
	"sort"
	"strings"
)

// Policy 客户端路由策略。
// DirectDomains: 命中即 direct 的域名(面板/订阅域名, 精确或后缀)。
type Policy struct {
	Main          string   `json:"main"` // 代理出站 tag(固定 "proxy")
	DirectDomains []string `json:"directDomains"`
}

// Default 构造默认策略: panelDomain 及其子域直连。
func Default(panelDomain string) Policy {
	domains := []string{}
	if panelDomain = strings.TrimSpace(panelDomain); panelDomain != "" {
		domains = append(domains, panelDomain, "*."+panelDomain)
	}
	return Policy{Main: "proxy", DirectDomains: domains}
}

// AddDirect 在策略中追加直连域名, 返回新 Policy 并保持排序稳定(便于 golden 对账)。
func (p Policy) AddDirect(domains ...string) Policy {
	seen := map[string]bool{}
	for _, d := range append(append([]string{}, p.DirectDomains...), domains...) {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" && !seen[d] {
			seen[d] = true
			p.DirectDomains = append(p.DirectDomains, d)
		}
	}
	sort.Strings(p.DirectDomains)
	return p
}

// IsDirect 判断域名是否命中直连(支持 exact 与 .suffix 通配)。
func (p Policy) IsDirect(domain string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return false
	}
	for _, rule := range p.DirectDomains {
		if rule == domain {
			return true
		}
	}
	for _, rule := range p.DirectDomains {
		if host, ok := wildcardBase(rule); ok && strings.HasSuffix(domain, "."+host) {
			return true
		}
	}
	return false
}

// wildcardBase 把 "*.example.com" 还原为 "example.com"(仅前缀通配形态)。
func wildcardBase(rule string) (string, bool) {
	if host, ok := strings.CutPrefix(rule, "*."); ok {
		return host, true
	}
	return "", false
}
