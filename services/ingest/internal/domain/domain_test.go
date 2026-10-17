package domain

import (
	"os"
	"strconv"
	"testing"

	"vpnmaster/subparse"
)

// 去重键契约: 真实样本(已脱敏) 42 行中, distinct 指纹数 == distinct addr:port 组数
// (共享 host/sni/path/uuid 的同组节点必须坍缩为一个去重单元)。
func TestFingerprintCollapsesSameAddrPortGroups(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/real-sub-vpn4zx.txt")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	nodes, err := subparse.Parse(string(raw), "real-sub-vpn4zx")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(nodes) != 42 {
		t.Fatalf("nodes = %d, want 42", len(nodes))
	}
	apSet, fpSet := map[string]bool{}, map[string]bool{}
	for _, m := range nodes {
		apSet[m.Address+":"+strconv.Itoa(m.Port)] = true
		fpSet[NewFingerprint(m)] = true
	}
	if len(fpSet) != len(apSet) {
		t.Fatalf("distinct fingerprints=%d, want == distinct addr:port groups=%d", len(fpSet), len(apSet))
	}
}

// 指纹原料契约: adr|port|protocol|streamJSON|sni 逐字段入盐。
func TestFingerprintRawLayout(t *testing.T) {
	n := Node{
		Address:  "h.example.com",
		Port:     443,
		Protocol: ProtoVLess,
		Stream: Stream{
			User:       "uuid-1",
			Network:    "ws",
			SNI:        "sni.example.com",
			StreamJSON: `{"network":"ws"}`,
		},
	}
	want := "h.example.com|443|vless|{\"network\":\"ws\"}|sni.example.com"
	if got := FingerprintRaw(n); got != want {
		t.Fatalf("FingerprintRaw = %q, want %q", got, want)
	}
	if NewFingerprint(n) == "" {
		t.Fatal("NewFingerprint must be non-empty")
	}
}
