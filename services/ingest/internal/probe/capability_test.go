package probe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"vpnmaster/ingest/internal/db"
)

func TestBuildTrojanHandshake(t *testing.T) {
	const host = "api.anthropic.com"
	pkt := BuildTrojanHandshake("secret-pw", host, 443)
	h := sha256.New224()
	h.Write([]byte("secret-pw"))
	wantHash := hex.EncodeToString(h.Sum(nil))
	if len(wantHash) != 56 {
		t.Fatalf("sha224 hex len = %d, want 56", len(wantHash))
	}
	if !bytes.HasPrefix(pkt, []byte(wantHash+"\r\n")) {
		t.Fatalf("packet must start with hex(sha224(pwd))+CRLF: %q", pkt[:60])
	}
	rest := pkt[len(wantHash)+2:]
	want := []byte{0x01 /*CONNECT*/, 0x03 /*ATYP domain*/, byte(len(host))}
	want = append(want, []byte(host)...)
	want = append(want, 0x01, 0xBB /*443 BE16*/, '\r', '\n')
	if !bytes.Equal(rest, want) {
		t.Fatalf("request part = %v, want %v", rest, want)
	}
}

func TestClassifyClaudeResponse(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"区域放行(缺 key)", 401, `{"type":"error","error":{"type":"authentication_error","message":"x-api-key header is required"}}`, true},
		{"区域封锁", 403, `{"error":{"type":"forbidden","message":"Request not allowed"}}`, false},
		{"国家不可用文案", 403, `<html>Claude is not available in your country</html>`, false},
		{"空响应", 520, ``, false},
		{"成功响应也算放行", 200, `{"id":"msg_1","type":"message"}`, false}, // 无 key 不可能 200, 保守 false
	}
	for _, c := range cases {
		if got := ClassifyClaudeResponse(c.status, c.body); got != c.want {
			t.Errorf("%s: ClassifyClaudeResponse(%d, %.40q) = %v, want %v", c.name, c.status, c.body, got, c.want)
		}
	}
}

// 探测成功必须抓到叶子证书指纹(xray 26 trojan outbound 依赖 pin, allowInsecure 已移除)。
func TestProbeOneCapturesCertPin(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer ts.Close()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portStr)
	sum := sha256.Sum256(ts.Certificate().Raw)
	want := hex.EncodeToString(sum[:])

	p := NewProber(newMemStore(t))
	got := p.probeOne(context.Background(), db.NodeRow{Address: host, Port: port, SNI: "example.com"})
	if !got.OK {
		t.Fatalf("probeOne not ok: %+v", got)
	}
	if got.CertPin != want {
		t.Fatalf("CertPin = %s, want %s", got.CertPin, want)
	}
}
