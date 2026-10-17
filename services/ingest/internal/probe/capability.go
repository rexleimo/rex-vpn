package probe

// 能力探测(区别于存活探测): 经节点隧道真实访问目标服务, 判定节点对该服务是否可用。
// 首个能力 = Claude: Anthropic 按出口 IP 段做区域封锁, 存活探测(TLS 握手)测不出来——
// ccwu001 全池握手正常但 claude.ai 被 CF 无限质询(2026-10-17 实测 0/31)。
// 判据用 api.anthropic.com: 区域放行时缺 key 返回 authentication_error(JSON),
// 区域封锁返回 forbidden "Request not allowed"。结果落 nodes.claude_ok,
// 供 xraygen 生成 claude 域名专用均衡器(claude_lb)。
//
// MVP 仅实现 trojan 协议隧道(实测 Claude 可用的 WestData 节点即 trojan);
// vless 隧道需实现完整握手协议, 留待有"claude 可用的 vless 上游"时再加。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
)

const (
	// ClaudeProbeHost 能力探测目标(api 层判据比网页稳定: 网页有 CF 反爬干扰)。
	ClaudeProbeHost = "api.anthropic.com"
	claudeProbeBody = `{"model":"claude-3-haiku-20240307","max_tokens":1,"messages":[{"role":"user","content":"ping"}]}`
)

// BuildTrojanHandshake 构造 trojan CONNECT 首包(纯函数, 便于单测):
// hex(sha224(password)) + CRLF + cmd(0x01) + ATYP(0x03 域名) + len + host + port(BE16) + CRLF。
// 成功时服务端回 "\r\n" 两字节。
func BuildTrojanHandshake(password, host string, port int) []byte {
	h := sha256.New224()
	h.Write([]byte(password))
	var buf bytes.Buffer
	buf.WriteString(hex.EncodeToString(h.Sum(nil)))
	buf.WriteString("\r\n")
	buf.WriteByte(0x01) // CONNECT
	buf.WriteByte(0x03) // ATYP: domain
	buf.WriteByte(byte(len(host)))
	buf.WriteString(host)
	buf.WriteByte(byte(port >> 8))
	buf.WriteByte(byte(port & 0xff))
	buf.WriteString("\r\n")
	return buf.Bytes()
}

// ClassifyClaudeResponse 判定 Anthropic API 响应 = 区域是否放行(纯函数, 便于单测)。
// 只认 authentication_error(说明请求已被 Anthropic 业务层受理, 区域检查通过);
// 其余(forbidden/区域文案/未知)一律 false, 保守不误标。
func ClassifyClaudeResponse(status int, body string) bool {
	_ = status
	return strings.Contains(body, "authentication_error")
}

// optAckConn 处理 trojan 服务端的两种 ack 行为: 标准实现立即回 "\r\n";
// 懒 ack 实现要等首个 payload 才回(实测 WestData: 阻塞等 ack 会死锁到超时)。
// 首次 Read 时尝试吃掉 CRLF; 若开头不是 CRLF(如 TLS 记录 0x16 0x03)则原样透传。
// 调用方必须先写入 header 后立即发起内层 TLS 握手(先写后读, 流水线), 不能阻塞等 ack。
type optAckConn struct {
	net.Conn
	peeked bool
	buf    []byte
}

func (c *optAckConn) Read(p []byte) (int, error) {
	if !c.peeked {
		c.peeked = true
		head := make([]byte, 2)
		n, err := io.ReadFull(c.Conn, head)
		if n == 2 && head[0] == '\r' && head[1] == '\n' {
			// 标准 ack, 吃掉继续读后续数据
		} else if n > 0 {
			c.buf = head[:n]
			if err != nil && err != io.ErrUnexpectedEOF {
				return copy(p, c.buf), err
			}
		} else {
			return 0, err
		}
	}
	if len(c.buf) > 0 {
		n := copy(p, c.buf)
		c.buf = c.buf[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

// TrojanClaudeOK 经 trojan 节点隧道访问 Anthropic API, 返回该节点是否 claude 可用。
// password 取 subparse 归一化的 Stream.User; sni 为空时用节点地址。
// 外层 TLS 用 uTLS 浏览器指纹(与 xray 客户端对齐); ack 用 optAckConn 兼容懒 ack 服务端。
func TrojanClaudeOK(ctx context.Context, addr string, port int, password, sni string, timeout time.Duration) (bool, error) {
	if sni == "" {
		sni = addr
	}
	d := &net.Dialer{Timeout: timeout}
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(addr, strconv.Itoa(port)))
	if err != nil {
		return false, err
	}
	outer := utls.UClient(raw, &utls.Config{ServerName: sni, InsecureSkipVerify: true}, utls.HelloChrome_Auto)
	if err := outer.HandshakeContext(ctx); err != nil {
		raw.Close()
		return false, err
	}
	defer outer.Close()
	_ = outer.SetDeadline(time.Now().Add(timeout))
	if _, err := outer.Write(BuildTrojanHandshake(password, ClaudeProbeHost, 443)); err != nil {
		return false, err
	}
	_ = outer.SetDeadline(time.Time{})
	// Transport 自己在隧道流上做 TLS(SNI 取自 URL); 切勿再包 tls.Client —— 双层 TLS 会报
	// "server gave HTTP response to HTTPS client"(实测踩坑)。
	stream := &optAckConn{Conn: outer}
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:       func(context.Context, string, string) (net.Conn, error) { return stream, nil },
			DisableKeepAlives: true,
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+ClaudeProbeHost+"/v1/messages", strings.NewReader(claudeProbeBody))
	if err != nil {
		return false, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return ClassifyClaudeResponse(resp.StatusCode, string(body)), nil
}
