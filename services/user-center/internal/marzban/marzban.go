// Package marzban 是 user-center 的数据层: 服务端代理 Marzban API, 不建用户数据后端。
// 接口化以便单测 mock(wc-1.3 验收: 单测不依赖真实 Marzban)。
// 真实字段/端点以 work-6.5 冒烟契约为准, 契约漂移只改 httpClient。
package marzban

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// User 是 Marzban 用户的最小字段集(流量/配额/到期/在线)。
type User struct {
	ID           int64
	Username     string
	Status       string // "active" | "banned"
	ExpireTime   int64  // unix, 0=无限期
	LimitTraffic int64  // 字节配额, 0=不限量
	UsedTraffic  int64  // 已用字节
	Online       bool
}

// Invoice 是 Marzban 发票/订单。
type Invoice struct {
	ID        int64
	PlanName  string
	Amount    int64  // 最小货币单位
	Status    string // "pending" | "paid"
	CreatedAt int64  // unix
}

// Client 是数据层抽象。MVP 仅读用户/订阅/发票; 购买走 CreateInvoice。
type Client interface {
	// GetUserByToken 按订阅 token 解析用户; token 无效/不存在返回 ErrNotFound。
	GetUserByToken(ctx context.Context, token string) (User, error)
	// SubURL 返回订阅链接(Clash YAML sub), 供复制/二维码。
	SubURL(token string) string
	// SubContent 拉取订阅原始内容(Clash YAML), 供服务端渲染。
	SubContent(ctx context.Context, token string) (string, error)
	// CreateInvoice 建订单(Marzban 侧), 到账由 Marzban 处理。
	CreateInvoice(ctx context.Context, token, plan string) (Invoice, error)
	// ListInvoices 订单历史。
	ListInvoices(ctx context.Context, token string) ([]Invoice, error)
}

// ErrNotFound 表示 token 无效或用户不存在。
var ErrNotFound = fmt.Errorf("marzban: user not found")

// Plans 是购买页 3 档套餐(可 env 覆盖)。
type Plan struct {
	Name  string
	Price int64 // 最小货币单位
	Bytes int64 // 流量
	Days  int
}

// PlansFromEnv 读 env USERCENTER_PLANS(形如 "name:price:bytes:days;...") 覆盖默认 3 档。
func PlansFromEnv() []Plan {
	def := []Plan{
		{Name: "轻量 10GB/月", Price: 990, Bytes: 10 << 30, Days: 30},
		{Name: "标准 50GB/月", Price: 3990, Bytes: 50 << 30, Days: 30},
		{Name: "畅享 200GB/月", Price: 11990, Bytes: 200 << 30, Days: 30},
	}
	if raw := os.Getenv("USERCENTER_PLANS"); raw != "" {
		var out []Plan
		for _, item := range strings.Split(raw, ";") {
			parts := strings.Split(item, ":")
			if len(parts) != 4 {
				continue
			}
			p := Plan{Name: parts[0]}
			fmt.Sscanf(parts[1], "%d", &p.Price)
			fmt.Sscanf(parts[2], "%d", &p.Bytes)
			fmt.Sscanf(parts[3], "%d", &p.Days)
			out = append(out, p)
		}
		if len(out) > 0 {
			return out
		}
	}
	return def
}

// Disabled 是一个返回 ErrNotFound 的桩 client, 供 Marzban 未配置/未联调时使用(避免 nil 接口 panic)。
type disabled struct{}

func (disabled) GetUserByToken(ctx context.Context, token string) (User, error) {
	return User{}, ErrNotFound
}
func (disabled) SubURL(token string) string { return "" }
func (disabled) SubContent(ctx context.Context, token string) (string, error) {
	return "", ErrNotFound
}
func (disabled) CreateInvoice(ctx context.Context, token, plan string) (Invoice, error) {
	return Invoice{}, ErrNotFound
}
func (disabled) ListInvoices(ctx context.Context, token string) ([]Invoice, error) {
	return nil, ErrNotFound
}

// Disabled 返回禁用桩。
func Disabled() Client { return disabled{} }

// NewHTTP 从 env 构造真实 HTTP client; MARZBAN_API_BASE 未配置返回 nil。
func NewHTTP() Client {
	base := strings.TrimRight(os.Getenv("MARZBAN_API_BASE"), "/")
	if base == "" {
		return nil
	}
	return &httpClient{
		apiBase: base,
		subBase: strings.TrimRight(os.Getenv("MARZBAN_SUB_BASE"), "/"),
		http:    &http.Client{Timeout: 20 * time.Second},
	}
}

type httpClient struct {
	apiBase string
	subBase string
	http    *http.Client
}

func (c *httpClient) SubURL(token string) string {
	if c.subBase != "" {
		return fmt.Sprintf("%s/sub/%s", c.subBase, token)
	}
	return fmt.Sprintf("%s/sub/%s", c.apiBase, token)
}

// marzbanUser 是 Marzban /api/users 响应的字段子集(字段名随版本, 尽力解析)。
type marzbanUser struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	Status       string `json:"status"`
	ExpireTime   int64  `json:"expire_time"`
	LimitTraffic int64  `json:"limit_traffic"`
	UsedTraffic  int64  `json:"used_traffic"`
	Online       bool   `json:"online"`
}

func (c *httpClient) GetUserByToken(ctx context.Context, token string) (User, error) {
	// Marzban 用户按 token 解析: 端点以 work-6.5 冒烟契约为准, MVP 走 /api/users?token=。
	u, err := getJSON[marzbanUser](c, ctx, c.apiBase+"/api/users?token="+url.QueryEscape(token))
	if err != nil {
		if errorsIsNotFound(err) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	if u.ID == 0 && u.Username == "" {
		return User{}, ErrNotFound
	}
	return User{
		ID: u.ID, Username: u.Username, Status: u.Status,
		ExpireTime: u.ExpireTime, LimitTraffic: u.LimitTraffic,
		UsedTraffic: u.UsedTraffic, Online: u.Online,
	}, nil
}

func (c *httpClient) SubContent(ctx context.Context, token string) (string, error) {
	// /sub/{token} 直接返回订阅内容(Clash YAML 或链接列表)。
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.SubURL(token), nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("marzban sub: %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

type marzbanInvoice struct {
	ID        int64  `json:"id"`
	PlanName  string `json:"plan_name"`
	Amount    int64  `json:"amount"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
}

func (c *httpClient) CreateInvoice(ctx context.Context, token, plan string) (Invoice, error) {
	// POST /api/invoices {token, plan}; 契约以 work-6 为准。
	body, _ := json.Marshal(map[string]string{"token": token, "plan": plan})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase+"/api/invoices", strings.NewReader(string(body)))
	if err != nil {
		return Invoice{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return Invoice{}, err
	}
	defer resp.Body.Close()
	var inv marzbanInvoice
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&inv); err != nil {
		return Invoice{}, err
	}
	return toInvoice(inv), nil
}

func (c *httpClient) ListInvoices(ctx context.Context, token string) ([]Invoice, error) {
	items, err := getJSONSlice[marzbanInvoice](c, ctx, c.apiBase+"/api/invoices?token="+url.QueryEscape(token))
	if err != nil {
		return nil, err
	}
	out := make([]Invoice, 0, len(items))
	for _, i := range items {
		out = append(out, toInvoice(i))
	}
	return out, nil
}

func toInvoice(m marzbanInvoice) Invoice {
	return Invoice{ID: m.ID, PlanName: m.PlanName, Amount: m.Amount, Status: m.Status, CreatedAt: m.CreatedAt}
}

// getJSON GET 一个对象并反序列化; 404 → ErrNotFound。
func getJSON[T any](c *httpClient, ctx context.Context, rawURL string) (T, error) {
	var out T
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return out, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return out, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("marzban: %s: %s", rawURL, resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

func getJSONSlice[T any](c *httpClient, ctx context.Context, rawURL string) ([]T, error) {
	// Marzban 列表可能直接是数组或 {data: [...]} 包壳, 两种都兼容。
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("marzban: %s: %s", rawURL, resp.Status)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var direct []T
	if err := json.Unmarshal(body, &direct); err == nil && len(direct) > 0 {
		return direct, nil
	}
	var wrap struct {
		Data []T `json:"data"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, err
	}
	return wrap.Data, nil
}

func errorsIsNotFound(err error) bool { return err == ErrNotFound }
