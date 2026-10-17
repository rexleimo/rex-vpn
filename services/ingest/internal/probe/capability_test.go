package probe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
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

// scriptConn 是只实现 Read 的脚本化连接(optAckConn 仅消费 Read)。
type scriptConn struct {
	net.Conn
	r io.Reader
}

func (c scriptConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func drainAck(t *testing.T, script string, chunk int) []byte {
	t.Helper()
	c := &optAckConn{Conn: scriptConn{r: strings.NewReader(script)}}
	var got []byte
	buf := make([]byte, chunk)
	for {
		n, err := c.Read(buf)
		got = append(got, buf[:n]...)
		if err == io.EOF {
			return got
		}
		if err != nil {
			t.Fatalf("read: %v", err)
		}
	}
}

func TestOptAckConn_StandardAckConsumed(t *testing.T) {
	// 标准 trojan 服务端: 立即回 CRLF, 之后是隧道数据 —— ack 必须被吃掉
	got := drainAck(t, "\r\nHELLO", 64)
	if string(got) != "HELLO" {
		t.Fatalf("got %q, want %q", got, "HELLO")
	}
}

func TestOptAckConn_LazyAckPassthrough(t *testing.T) {
	// 懒 ack 服务端(WestData 实测): 不回 CRLF, 首读即隧道数据(TLS 记录头 0x16 0x03)
	// —— 首两字节不是 CRLF 时必须原样透传, 一个字节都不能丢
	script := "\x16\x03\x01\x00\x05abc"
	got := drainAck(t, script, 64)
	if string(got) != script {
		t.Fatalf("got %q, want %q", got, script)
	}
}

func TestOptAckConn_AmbiguousCRPrefix(t *testing.T) {
	// 数据恰好以 \r 开头但第二字节不是 \n: 两字节都属于数据
	got := drainAck(t, "\rXabc", 64)
	if string(got) != "\rXabc" {
		t.Fatalf("got %q", got)
	}
}

func TestOptAckConn_ByteAtATimeReassembly(t *testing.T) {
	// 小块读取: 透传缓冲必须完整排空后才回到底层连接
	got := drainAck(t, "\r\nWORLD", 1)
	if string(got) != "WORLD" {
		t.Fatalf("got %q", got)
	}
}

func TestOptAckConn_ImmediateEOF(t *testing.T) {
	// 服务端直接断开: 首读干净地返回 EOF, 不挂起不 panic
	c := &optAckConn{Conn: scriptConn{r: strings.NewReader("")}}
	n, err := c.Read(make([]byte, 8))
	if n != 0 || err != io.EOF {
		t.Fatalf("n=%d err=%v, want 0/EOF", n, err)
	}
}
