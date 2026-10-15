// Package marzban 是 user-center 的数据层: 服务端代理 Marzban API, 不建用户数据后端。
// 接口化以便单测 mock(wc-1.3 验收: 单测不依赖真实 Marzban)。
//
// 契约(经 work-6.1/6.2 本地 Marzban 0.44+ 实测, 见 docs/marzban-checklist.md):
//   - admin 鉴权: POST /api/admin/token (form: username/password) → access_token(JWT), 无 /api/login
//   - 用户查询:   GET /api/user/{username} (单数, 非 /api/users?token=)
//   - 订阅 token: /sub/{base64url(username)+10位hmac}, 本地可解码出 username; 由运营发给用户
//   - 订阅内容:   GET /sub/{token}, 按 User-Agent 选格式(clash-verge/stash → Clash YAML)
//   - 用户字段:   expire(天) / data_limit(字节) / used_traffic; 创建用户需 proxies, 建议显式绑 inbounds
//   - 发票:       本版本无 invoice API → 购买页 MVP 仅"联系渠道加量"(与 plan §0 决策一致)
package marzban

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// User 是 Marzban 用户的最小字段集(流量/配额/到期/在线)。
type User struct {
	ID           int64
	Username     string
	Status       string // "active" | "banned" | "expired"
	ExpireDays   int    // 套餐时长(天), 0=不限时
	LimitTraffic int64  // 字节配额, 0=不限量
	UsedTraffic  int64  // 已用字节
	Online       bool
}

// Invoice 是 Marzban 发票/订单(预留: 当前版本 API 未提供, 接口保留供 Phase 2)。
type Invoice struct {
	ID        int64
	PlanName  string
	Amount    int64
	Status    string
	CreatedAt int64
}

// ErrNotSupported 表示当前 Marzban 版本无该能力(如发票)。
var ErrNotSupported = errors.New("marzban: not supported in this version")

// Client 是数据层抽象。MVP: 只读用户/订阅 + 发票接口(暂 NotImplemented)。
type Client interface {
	// GetUserByToken 按订阅 token 解析用户; token 无效/不存在返回 ErrNotFound。
	GetUserByToken(ctx context.Context, token string) (User, error)
	// SubURL 返回订阅链接(Clash YAML sub), 供复制/二维码。
	SubURL(token string) string
	// SubContent 拉取订阅原始内容(按 UA 选格式, 默认 Clash YAML)。
	SubContent(ctx context.Context, token, userAgent string) (string, error)
	// CreateInvoice 建订单(Marzban invoice API); 当前版本不支持 → ErrNotSupported。
	CreateInvoice(ctx context.Context, token, plan string) (Invoice, error)
	// ListInvoices 订单历史; 当前版本不支持 → 空列表。
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

// NewHTTP 从 env 构造真实 HTTP client; MARZBAN_API_BASE 未配置返回 nil。
func NewHTTP() Client {
	base := strings.TrimRight(os.Getenv("MARZBAN_API_BASE"), "/")
	if base == "" {
		return nil
	}
	return &httpClient{
		base:      base,
		adminU:    os.Getenv("MARZBAN_ADMIN_USER"),
		adminP:    os.Getenv("MARZBAN_ADMIN_PASSWORD"),
		http:      &http.Client{Timeout: 20 * time.Second},
		subUA:     os.Getenv("MARZBAN_SUB_UA"),
	}
}

// decodeSubToken 本地解码 Marzban 订阅 token → username。
// 格式: base64url("username,\"+ceil(now)\", altchars=-_, 无 padding) + 10 位 HMAC(前缀)。
// HMAC 无法本地校验(密钥在 Marzban jwt 表), 由后续 /sub 拉取 + /api/user 查询兜底。
func decodeSubToken(token string) (string, error) {
	if len(token) < 11 {
		return "", ErrNotFound
	}
	payload := token[:len(token)-10]
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", ErrNotFound
	}
	// payload = "username,timestamp", 取首段
	username := strings.TrimSpace(strings.Split(string(b), ",")[0])
	if username == "" {
		return "", ErrNotFound
	}
	return username, nil
}

type httpClient struct {
	base   string
	adminU string
	adminP string
	http   *http.Client
	subUA  string // 拉订阅用的 UA(默认 clash-verge → Clash YAML)

	mu      sync.Mutex
	token   string
	tokenAt time.Time
}

// SubURL 返回订阅链接(走 API 同源; 线上由 Dokploy 反代对外)。
func (c *httpClient) SubURL(token string) string {
	return fmt.Sprintf("%s/sub/%s", c.base, token)
}

// adminToken 登录(POST /api/admin/token, form 编码) + 23h 缓存。
func (c *httpClient) adminToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Since(c.tokenAt) < 23*time.Hour {
		return c.token, nil
	}
	if c.adminU == "" || c.adminP == "" {
		return "", fmt.Errorf("MARZBAN_ADMIN_USER/MARZBAN_ADMIN_PASSWORD 未配置")
	}
	form := url.Values{"username": {c.adminU}, "password": {c.adminP}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/admin/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("marzban /api/admin/token: %s", resp.Status)
	}
	var j struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&j); err != nil {
		return "", err
	}
	if j.AccessToken == "" {
		return "", fmt.Errorf("marzban login: empty access_token")
	}
	c.token = j.AccessToken
	c.tokenAt = time.Now()
	return c.token, nil
}

// marzbanUser 是 GET /api/user/{username} 响应字段子集(work-6.2 实测)。
type marzbanUser struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Status      string `json:"status"`
	Expire      int    `json:"expire"` // 天, null=不限时
	DataLimit   int64  `json:"data_limit"`
	UsedTraffic int64  `json:"used_traffic"`
	OnlineAt    string `json:"online_at"`
}

// GetUserByToken: 本地解码 token → username, 再经 admin API 查用户。
func (c *httpClient) GetUserByToken(ctx context.Context, token string) (User, error) {
	username, err := decodeSubToken(token)
	if err != nil {
		return User{}, err
	}
	adm, err := c.adminToken(ctx)
	if err != nil {
		return User{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/user/"+url.PathEscape(username), nil)
	if err != nil {
		return User{}, err
	}
	req.Header.Set("Authorization", "Bearer "+adm)
	resp, err := c.http.Do(req)
	if err != nil {
		return User{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return User{}, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return User{}, fmt.Errorf("marzban /api/user/%s: %s", username, resp.Status)
	}
	var u marzbanUser
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&u); err != nil {
		return User{}, err
	}
	return User{
		ID: u.ID, Username: u.Username, Status: u.Status,
		ExpireDays: u.Expire, LimitTraffic: u.DataLimit, UsedTraffic: u.UsedTraffic,
		Online: u.OnlineAt != "" && u.OnlineAt != "null",
	}, nil
}

// SubContent 拉订阅内容; UA 默认 clash-verge(Clash YAML), 可用 MARZBAN_SUB_UA 覆盖。
func (c *httpClient) SubContent(ctx context.Context, token, userAgent string) (string, error) {
	ua := userAgent
	if ua == "" {
		ua = c.subUA
	}
	if ua == "" {
		ua = "clash-verge/1.0"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.SubURL(token), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", ua)
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

// CreateInvoice: 当前版本 Marzban 无 invoice API(MVP 购买=联系渠道加量)。
func (c *httpClient) CreateInvoice(ctx context.Context, token, plan string) (Invoice, error) {
	return Invoice{}, ErrNotSupported
}

// ListInvoices: 当前版本无 invoice API, 返回空列表。
func (c *httpClient) ListInvoices(ctx context.Context, token string) ([]Invoice, error) {
	return nil, nil
}

// Disabled 是返回 ErrNotFound 的桩 client, 供 Marzban 未配置/未联调时使用(避免 nil 接口 panic)。
type disabled struct{}

func (disabled) GetUserByToken(ctx context.Context, token string) (User, error) {
	return User{}, ErrNotFound
}
func (disabled) SubURL(token string) string { return "" }
func (disabled) SubContent(ctx context.Context, token, userAgent string) (string, error) {
	return "", ErrNotFound
}
func (disabled) CreateInvoice(ctx context.Context, token, plan string) (Invoice, error) {
	return Invoice{}, ErrNotSupported
}
func (disabled) ListInvoices(ctx context.Context, token string) ([]Invoice, error) {
	return nil, nil
}

// Disabled 返回禁用桩。
func Disabled() Client { return disabled{} }

