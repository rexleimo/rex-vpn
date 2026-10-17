package subparse

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// parseLinkList 解析逐行 "scheme://..." 纯链接订阅。
// 支持 vless / vmess / shadowsocks / trojan; 空行与非代理行跳过, 坏行跳过并计数。
func parseLinkList(content, sourceTag string) ([]Node, error) {
	var nodes []Node
	skipped := 0
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		n, ok, err := parseOneLine(line, sourceTag)
		if err != nil {
			skipped++
			continue
		}
		if !ok {
			continue
		}
		nodes = append(nodes, n)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("sub: link list yielded 0 proxy lines (skipped %d bad lines)", skipped)
	}
	return nodes, nil
}

func parseOneLine(line, sourceTag string) (Node, bool, error) {
	switch {
	case strings.HasPrefix(line, "vless://"):
		return parseVLESS(line, sourceTag)
	case strings.HasPrefix(line, "vmess://"):
		return parseVMESS(line, sourceTag)
	case strings.HasPrefix(line, "ss://"):
		return parseShadowsocks(line, sourceTag)
	case strings.HasPrefix(line, "trojan://"):
		return parseTrojan(line, sourceTag)
	default:
		return Node{}, false, nil
	}
}

// parseVLESS: vless://UUID@host:port?security=tls&type=ws&host=X&fp=chrome&sni=X&path=%2F&flow=...#tag
func parseVLESS(line, sourceTag string) (Node, bool, error) {
	body, frag := splitFragment(strings.TrimPrefix(line, "vless://"))
	u, err := url.Parse("vless://" + body)
	if err != nil {
		return Node{}, false, fmt.Errorf("vless url: %w", err)
	}
	q := u.Query()
	node := Node{
		Address:  u.Hostname(),
		Port:     atoiDefault(u.Port(), 0),
		Protocol: ProtoVLess,
		Tag:      orDefault(frag, sourceTag),
		Stream: Stream{
			User:    u.User.String(),
			Network: orDefault(q.Get("type"), "tcp"),
			Finger:  q.Get("fp"),
		},
	}
	node.Stream.SNI = orDefault(q.Get("sni"), q.Get("host"))
	if node.Stream.Network == "ws" {
		node.Stream.WSPath = orDefault(q.Get("path"), "/")
	}
	node.Stream.StreamJSON = streamJSON(node)
	return node, true, nil
}

// parseVMESS: vmess://BASE64(JSON{add,port,id,ps,aid,scy,net,host,path,tls,sni,fp})
func parseVMESS(line, sourceTag string) (Node, bool, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "vmess://"))
	if err != nil {
		return Node{}, false, fmt.Errorf("vmess base64: %w", err)
	}
	var j struct {
		Add  string `json:"add"`
		Port string `json:"port"`
		ID   string `json:"id"`
		Ps   string `json:"ps"`
		Net  string `json:"net"`
		Sni  string `json:"sni"`
		Fp   string `json:"fp"`
		Path string `json:"path"`
		Host string `json:"host"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return Node{}, false, fmt.Errorf("vmess json: %w", err)
	}
	node := Node{
		Address:  j.Add,
		Port:     atoiDefault(j.Port, 0),
		Protocol: ProtoVMESS,
		Tag:      orDefault(j.Ps, sourceTag),
		Stream: Stream{
			User:    j.ID,
			Network: orDefault(j.Net, "tcp"),
			SNI:     orDefault(j.Sni, j.Host),
			Finger:  j.Fp,
			WSPath:  j.Path,
		},
	}
	node.Stream.StreamJSON = streamJSON(node)
	return node, true, nil
}

// parseShadowsocks: ss://BASE64(method:password@host:port)#tag
// 兼容未 base64 的裸 URI 变体 ss://method:pass@host:port。
func parseShadowsocks(line, sourceTag string) (Node, bool, error) {
	body, frag := splitFragment(strings.TrimPrefix(line, "ss://"))
	if decoded, err := base64.StdEncoding.DecodeString(body); err == nil && strings.Contains(string(decoded), "@") {
		text := string(decoded)
		var method, cred, hostport string
		if idx := strings.LastIndexByte(text, '@'); idx >= 0 {
			cred, hostport = text[:idx], text[idx+1:]
			if ci := strings.IndexByte(cred, ':'); ci >= 0 {
				method, cred = cred[:ci], cred[ci+1:]
			} else {
				method = "auto"
			}
		}
		host, port := hostport, 0
		if h, p, ok := splitHostPort(hostport); ok {
			host, port = h, atoiDefault(p, 0)
		}
		node := Node{
			Address:  host,
			Port:     port,
			Protocol: ProtoShadow,
			Tag:      orDefault(frag, sourceTag),
			Stream:   Stream{User: method + ":" + cred, Network: "tcp"},
		}
		node.Stream.StreamJSON = streamJSON(node)
		return node, true, nil
	}
	// 裸 URI 变体
	u, err := url.Parse("ss://" + body)
	if err != nil {
		return Node{}, false, fmt.Errorf("ss uri: %w (also not valid base64)", err)
	}
	host, port := u.Hostname(), 0
	if h, p, ok := splitHostPort(u.Host); ok {
		host, port = h, atoiDefault(p, 0)
	}
	node := Node{
		Address:  host,
		Port:     port,
		Protocol: ProtoShadow,
		Tag:      orDefault(frag, sourceTag),
		Stream:   Stream{User: u.User.String(), Network: "tcp"},
	}
	node.Stream.StreamJSON = streamJSON(node)
	return node, true, nil
}

// parseTrojan: trojan://pass@host:port?security=tls&sni=X&type=ws&path=...#tag
func parseTrojan(line, sourceTag string) (Node, bool, error) {
	body, frag := splitFragment(strings.TrimPrefix(line, "trojan://"))
	u, err := url.Parse("trojan://" + body)
	if err != nil {
		return Node{}, false, fmt.Errorf("trojan url: %w", err)
	}
	q := u.Query()
	node := Node{
		Address:  u.Hostname(),
		Protocol: ProtoTrojan,
		Tag:      orDefault(frag, sourceTag),
		Stream: Stream{
			User:    u.User.String(),
			Network: orDefault(q.Get("type"), "tcp"),
			SNI:     orDefault(q.Get("sni"), q.Get("host")),
			Finger:  q.Get("fp"),
			WSPath:  q.Get("path"),
		},
	}
	if h, p, ok := splitHostPort(u.Host); ok {
		_, node.Port = h, atoiDefault(p, 0)
	}
	node.Stream.StreamJSON = streamJSON(node)
	return node, true, nil
}

// ---- helpers ----

func splitFragment(s string) (body, frag string) {
	if i := strings.IndexByte(s, '#'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// splitHostPort 拆分 host:port, 兼容 [v6]:port。
func splitHostPort(s string) (host, port string, ok bool) {
	if strings.HasPrefix(s, "[") {
		if i := strings.IndexByte(s, ']'); i > 0 {
			rest := s[i+1:]
			if strings.HasPrefix(rest, ":") {
				return s[1:i], rest[1:], true
			}
			return s[1:i], "", true
		}
	}
	if i := strings.LastIndexByte(s, ':'); i > 0 {
		return s[:i], s[i+1:], true
	}
	return s, "", false
}

func atoiDefault(s string, def int) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// streamJSON 生成 xraygen(ingest work-5) 与客户端 cfggen 直填 outbound 的
// 传输层 JSON(含协议凭证)。
func streamJSON(n Node) string {
	src := map[string]any{"network": n.Stream.Network}
	if n.Stream.User != "" {
		src["user"] = n.Stream.User
	}
	if n.Stream.SNI != "" {
		src["sni"] = n.Stream.SNI
	}
	if n.Stream.Finger != "" {
		src["fp"] = n.Stream.Finger
	}
	if n.Stream.WSPath != "" && n.Stream.Network == "ws" {
		src["wsPath"] = n.Stream.WSPath
	}
	b, _ := json.Marshal(src)
	return string(b)
}
