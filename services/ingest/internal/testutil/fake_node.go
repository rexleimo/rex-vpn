// Package testutil 提供探测/联调用的假节点（自签 TLS listener）与工具。
package testutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"sync"
	"time"
)

// FakeNode 是一个自签 TLS 服务, 模拟上游 vless+tls 节点, 供探测单测驱动。
// 握手成功即可; 可被"关停"(Closed=true 后拒连) 模拟节点故障。
type FakeNode struct {
	Addr   string // host:port
	Port   int
	Lis    net.Listener
	cert   tls.Certificate
	mu     sync.Mutex
	Closed bool
	closed chan struct{}
}

// NewFakeNode 起一个自签 TLS listener。
func NewFakeNode(t interface {
	Helper()
	Cleanup(func())
}, hostname string) (*FakeNode, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: hostname},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{hostname},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	_, port, _ := net.SplitHostPort(lis.Addr().String())
	pn := &FakeNode{Port: atoi(port), cert: cert, Lis: lis, closed: make(chan struct{})}
	pn.Addr = "127.0.0.1:" + port

	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				select {
				case <-pn.closed:
					return
				default:
					continue
				}
			}
			pn.mu.Lock()
			closed := pn.Closed
			pn.mu.Unlock()
			if closed {
				_ = conn.Close()
				continue
			}
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}})
			_ = tlsConn.Handshake()
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { pn.Close() })
	return pn, nil
}

// Close 关停 listener。
func (f *FakeNode) Close() {
	close(f.closed)
	_ = f.Lis.Close()
}

// MarkDown 让后续连接被拒(模拟故障, 不断 listener)。
func (f *FakeNode) MarkDown() {
	f.mu.Lock()
	f.Closed = true
	f.mu.Unlock()
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}
