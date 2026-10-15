package sub

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"vpnmaster/ingest/internal/domain"
)

// 每协议 ≥1 fixture（3.3 验收）+ base64 包裹 + 非法输入断言。

func TestParseVLESS(t *testing.T) {
	raw := "vless://01234567-aaaa-bbbb-cccc-000000000001@example.com:443?security=tls&type=ws&host=example.com&fp=chrome&sni=example.com&path=%2Fws%2F&flow=xtls-rprx-vision#MyNode\n"
	nodes, err := Parse(raw, "tag")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}
	n := nodes[0]
	if n.Address != "example.com" || n.Port != 443 || n.Protocol != domain.ProtoVLess {
		t.Fatalf("node = %+v", n)
	}
	if n.Stream.User != "01234567-aaaa-bbbb-cccc-000000000001" || n.Stream.Network != "ws" {
		t.Fatalf("stream = %+v", n.Stream)
	}
	if n.Stream.SNI != "example.com" || n.Stream.WSPath != "/ws/" || n.Stream.Finger != "chrome" {
		t.Fatalf("stream = %+v", n.Stream)
	}
	if n.Tag != "MyNode" {
		t.Fatalf("Tag = %q, want MyNode", n.Tag)
	}
	var j map[string]any
	if err := json.Unmarshal([]byte(n.Stream.StreamJSON), &j); err != nil {
		t.Fatalf("StreamJSON not json: %v", err)
	}
	if j["network"] != "ws" || j["user"] != "01234567-aaaa-bbbb-cccc-000000000001" || j["sni"] != "example.com" {
		t.Fatalf("StreamJSON = %v", j)
	}
}

func TestParseVMESS(t *testing.T) {
	j := `{"add":"vm.example.com","port":"8443","id":"vm-uuid-1","ps":"VM1","net":"ws","host":"vm.example.com","path":"/vm","tls":"1","sni":"vm.example.com","fp":"chrome"}`
	raw := "vmess://" + base64.StdEncoding.EncodeToString([]byte(j)) + "\n"
	nodes, err := Parse(raw, "tag")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	n := nodes[0]
	if n.Address != "vm.example.com" || n.Port != 8443 || n.Protocol != domain.ProtoVMESS || n.Stream.User != "vm-uuid-1" {
		t.Fatalf("node = %+v", n)
	}
	if n.Stream.SNI != "vm.example.com" || n.Stream.Network != "ws" || n.Stream.WSPath != "/vm" {
		t.Fatalf("stream = %+v", n.Stream)
	}
}

func TestParseShadowsocks(t *testing.T) {
	// base64(method:pass@host:port) 变体
	raw := "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:sspass@ss.example.com:8388")) + "#SS1\n"
	nodes, err := Parse(raw, "tag")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	n := nodes[0]
	if n.Address != "ss.example.com" || n.Port != 8388 || n.Protocol != domain.ProtoShadow {
		t.Fatalf("node = %+v", n)
	}
	if n.Stream.User != "aes-256-gcm:sspass" {
		t.Fatalf("User = %q, want method:password", n.Stream.User)
	}
}

func TestParseTrojan(t *testing.T) {
	raw := "trojan://trojpass@troj.example.com:443?security=tls&sni=troj.example.com&type=ws&path=%2Ftroj#TR1\n"
	nodes, err := Parse(raw, "tag")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	n := nodes[0]
	if n.Address != "troj.example.com" || n.Port != 443 || n.Protocol != domain.ProtoTrojan {
		t.Fatalf("node = %+v", n)
	}
	if n.Stream.User != "trojpass" || n.Stream.SNI != "troj.example.com" || n.Stream.WSPath != "/troj" {
		t.Fatalf("stream = %+v", n.Stream)
	}
}

// base64 包裹的纯链接列表。
func TestParseBase64Wrapped(t *testing.T) {
	plain := "vless://01234567-aaaa-bbbb-cccc-000000000002@b64.example.com:443?security=tls&type=ws&sni=b64.example.com&path=%2F#B64\n"
	nodes, err := Parse(base64.StdEncoding.EncodeToString([]byte(plain)), "tag")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Address != "b64.example.com" || nodes[0].Tag != "B64" {
		t.Fatalf("nodes = %+v", nodes)
	}
}

func TestParseClashYAML(t *testing.T) {
	raw := "proxies:\n" +
		"  - name: CL-VLESS\n    type: vless\n    server: c1.example.com\n    port: 443\n    uuid: cl-uuid-1\n    network: ws\n    sni: c1.example.com\n    ws-opts:\n      path: /cl\n" +
		"  - name: CL-SS\n    type: shadowsocks\n    server: c2.example.com\n    port: 8388\n    password: cl-ss-pass\n    method: aes-256-gcm\n" +
		"  - name: CL-H2\n    type: hysteria2\n    server: c3.example.com\n    port: 443\n    password: x\n" // 白名单外, 应跳过
	nodes, err := Parse(raw, "tag")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2 (hysteria2 跳过)", len(nodes))
	}
	if nodes[0].Protocol != domain.ProtoVLess || nodes[0].Stream.User != "cl-uuid-1" {
		t.Fatalf("node0 = %+v", nodes[0])
	}
	if nodes[1].Protocol != domain.ProtoShadow || nodes[1].Stream.User != "aes-256-gcm:cl-ss-pass" {
		t.Fatalf("node1 = %+v", nodes[1])
	}
}

// 非法输入断言报错（3.3 验收）。
func TestParseInvalidInputs(t *testing.T) {
	cases := []string{
		"",
		"random text no scheme\nanother random line\n",
		"vmess://" + base64.StdEncoding.EncodeToString([]byte("not-json")),
		"vless://@:badport?security=tls", // 0 有效行 → 报错
	}
	for i, c := range cases {
		if c == "" {
			if _, err := Parse(c, "tag"); err == nil {
				t.Errorf("case %d: empty input should error", i)
			}
			continue
		}
		if _, err := Parse(c, "tag"); err == nil {
			t.Errorf("case %d: invalid input should error, got nil", i)
		}
	}
}

// 坏行跳过: 好坏混合, 好行保留、坏行不炸。
func TestParseSkipsBadLines(t *testing.T) {
	raw := "vless://01234567-aaaa-bbbb-cccc-000000000003@mix.example.com:443?security=tls&type=ws&sni=mix.example.com\nftp://nope\n\n"
	nodes, err := Parse(raw, "tag")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(nodes))
	}
}

// 3.5: 真实样本 fixture（已脱敏）: 节点数=42、字段 golden。
func TestRealSampleFixture(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/real-sub-vpn4zx.txt")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	nodes, err := Parse(string(raw), "real-sub-vpn4zx")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(nodes) != 42 {
		t.Fatalf("nodes = %d, want 42", len(nodes))
	}
	// golden: 首个节点字段
	n := nodes[0]
	if n.Address != "162.159.198.1" || n.Port != 443 || n.Protocol != domain.ProtoVLess {
		t.Fatalf("golden node[0] = %+v", n)
	}
	if n.Stream.SNI != "vpn-4zx.pages.dev" || n.Stream.Network != "ws" || n.Stream.Finger != "chrome" {
		t.Fatalf("golden stream[0] = %+v", n.Stream)
	}
	// 去重键: 脱敏后 42 行 = 14 个 distinct(addr,port) 组(共享 host/sni/path/uuid),
	// distinct 指纹数应 == distinct addr:port 组数(同组坍缩为一个节点)
	apSet, fpSet := map[string]bool{}, map[string]bool{}
	for _, m := range nodes {
		apSet[m.Address+":"+strconv.Itoa(m.Port)] = true
		fpSet[domain.NewFingerprint(m)] = true
	}
	if len(fpSet) != len(apSet) {
		t.Fatalf("distinct fingerprints=%d, want == distinct addr:port groups=%d", len(fpSet), len(apSet))
	}
}
