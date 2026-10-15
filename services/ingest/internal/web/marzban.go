package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

// MarzbanClient 是用户中台(Marzban)的只读管理接口(7.2 用户页)。
// 认证: POST /api/login(username,password) → JWT, 缓存 23h。
// 只暴露 ListUsers, 供管理页"用户(只读)"与未来客户端流量看板用。

// User 是 Marzban 用户的最小字段集(与 UserView 对齐)。
type User struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	Status     string `json:"status"`
	ExpireTime int64  `json:"expire_time"` // unix
	// 流量字段(未来看板用): 依 marzban 版本字段名可能不同, 尽力解析
	TotalUpload   int64 `json:"total_upload"`
	TotalDownload int64 `json:"total_download"`
}

type MarzbanClient struct {
	base    string
	adminU  string
	adminP  string
	client  *http.Client
	mu      sync.Mutex
	token   string
	tokenAt time.Time
}

// NewMarzbanClient 从 env 构造; MARZBAN_API_BASE 未配置返回 nil。
func NewMarzbanClient() *MarzbanClient {
	base := os.Getenv("MARZBAN_API_BASE")
	if base == "" {
		return nil
	}
	return &MarzbanClient{
		base:   base,
		adminU: os.Getenv("MARZBAN_ADMIN_USER"),
		adminP: os.Getenv("MARZBAN_ADMIN_PASSWORD"),
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

// Configured 是否可用。
func (c *MarzbanClient) Configured() bool { return c != nil && c.adminU != "" && c.adminP != "" }

// ListUsers 拉取用户列表(只读)。
func (c *MarzbanClient) ListUsers(ctx context.Context) ([]User, error) {
	if c == nil {
		return nil, fmt.Errorf("marzban not configured")
	}
	tok, err := c.ensureToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/users", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("marzban /api/users: %s: %s", resp.Status, truncate(string(body), 200))
	}
	// marzban 返回 {data: [...], ...} 或直接 [...], 兼容两种。
	var wrap struct {
		Data []User `json:"data"`
	}
	var direct []User
	if err := json.Unmarshal(body, &wrap); err == nil && len(wrap.Data) > 0 {
		return wrap.Data, nil
	}
	if err := json.Unmarshal(body, &direct); err != nil {
		return nil, fmt.Errorf("marzban users decode: %w", err)
	}
	return direct, nil
}

func (c *MarzbanClient) ensureToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Since(c.tokenAt) < 23*time.Hour {
		return c.token, nil
	}
	if c.adminU == "" || c.adminP == "" {
		return "", fmt.Errorf("MARZBAN_ADMIN_USER/PASSWORD 未配置")
	}
	form := url.Values{"username": {c.adminU}, "password": {c.adminP}}
	resp, err := c.client.PostForm(c.base+"/api/login", form)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("marzban login: %s: %s", resp.Status, truncate(string(body), 120))
	}
	var j struct {
		Token string `json:"token"`
		// 某些版本字段为 access_token
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &j); err != nil {
		return "", err
	}
	tok := j.Token
	if tok == "" {
		tok = j.AccessToken
	}
	if tok == "" {
		return "", fmt.Errorf("marzban login: empty token")
	}
	c.token = tok
	c.tokenAt = time.Now()
	return tok, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
