// Package sub 负责客户端侧订阅拉取(docs/client-impl.md §1)。
// GET {panel}/sub/{token} 的正文交给共享解析包 subparse 归一化,
// 保证"面板/上游/客户端"三方对同一订阅解析出同一形态(ADR-0005)。
package sub

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"vpnmaster/subparse"
)

// Client 拉取面板下发的订阅内容。
// HTTP 可注入(测试走 httptest, 生产带 15s 超时)。
type Client struct {
	HTTP *http.Client
}

// NewClient 返回默认 15s 超时的订阅客户端。
func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// Fetch 拉取并归一化订阅。panelBase 为面板基址(如 https://user.example.com),
// token 为面板订阅 token; URL 直接拼接 token 段, 不再参与查询参数。
func (c *Client) Fetch(panelBase, token string) ([]subparse.Node, error) {
	base := strings.TrimRight(panelBase, "/")
	if base == "" {
		return nil, fmt.Errorf("sub: empty panel base")
	}
	endpoint := base + "/sub/" + url.PathEscape(token)
	resp, err := c.HTTP.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("sub: GET %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sub: GET %s: status %d", endpoint, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2MB 上限防异常响应
	if err != nil {
		return nil, fmt.Errorf("sub: read body: %w", err)
	}
	nodes, err := subparse.Parse(string(raw), "panel-sub")
	if err != nil {
		return nil, fmt.Errorf("sub: %w", err)
	}
	return nodes, nil
}
