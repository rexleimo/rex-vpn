// Provisioning: 订阅付款后由系统创建/更新 Marzban 用户。
// 契约: POST /api/users(409 已存在→幂等回读), PUT /api/users/{username}(部分更新)。
// inbounds 来自 MARZBAN_INBOUND_IDS(逗号分隔 tag), 未配置时回退 "all" proxies。
package marzban

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// UserSpec 是创建用户参数。
type UserSpec struct {
	Username  string
	Expire    int64 // unix 秒, Marzban expire 字段
	DataLimit int64 // 字节, 0 = 不限
}

// Created 是 provision 结果: 面板生成的订阅 token 是付款的产物。
type Created struct {
	Username string
	Token    string
}

// UserMod 是部分更新参数(nil 字段不动)。
type UserMod struct {
	Expire      *int64
	DataLimit   *int64
	UsedTraffic *int64 // 续费归零传 0
	Status      string // 需要时 "active"/"disabled"
}

func inboundIDs() []string {
	raw := os.Getenv("MARZBAN_INBOUND_IDS")
	if raw == "" {
		return nil
	}
	var out []string
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (c *httpClient) CreateUser(ctx context.Context, spec UserSpec) (Created, error) {
	if spec.Username == "" {
		return Created{}, fmt.Errorf("marzban: create: empty username")
	}
	body := map[string]any{
		"username":   spec.Username,
		"status":     "active",
		"expire":     spec.Expire,
		"data_limit": spec.DataLimit,
	}
	if ids := inboundIDs(); len(ids) > 0 {
		body["inbounds"] = ids
	} else {
		body["proxies"] = "all"
	}
	resp, err := c.userRequest(ctx, http.MethodPost, "/api/users", body)
	if err != nil {
		return Created{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		// 幂等重试路径: 用户名已在(上次成功一半), 回读 token。
		return c.getUserByUsername(ctx, spec.Username)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return Created{}, fmt.Errorf("marzban POST /api/users: %s", resp.Status)
	}
	return decodeCreated(resp.Body)
}

func (c *httpClient) ModifyUser(ctx context.Context, username string, mod UserMod) error {
	body := map[string]any{}
	if mod.Expire != nil {
		body["expire"] = *mod.Expire
	}
	if mod.DataLimit != nil {
		body["data_limit"] = *mod.DataLimit
	}
	if mod.UsedTraffic != nil {
		body["used_traffic"] = *mod.UsedTraffic
		// Marzban 以 limit_used_traffic 表达显式重置语义, 一并携带。
		body["limit_used_traffic"] = *mod.UsedTraffic
	}
	if mod.Status != "" {
		body["status"] = mod.Status
	}
	resp, err := c.userRequest(ctx, http.MethodPut, "/api/users/"+url.PathEscape(username), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("marzban PUT /api/users/%s: %s", username, resp.Status)
	}
	return nil
}

// userRequest 带 admin JWT 发 JSON 请求。
func (c *httpClient) userRequest(ctx context.Context, method, path string, body any) (*http.Response, error) {
	adm, err := c.adminToken(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+adm)
	req.Header.Set("Content-Type", "application/json")
	return c.http.Do(req)
}

func (c *httpClient) getUserByUsername(ctx context.Context, username string) (Created, error) {
	adm, err := c.adminToken(ctx)
	if err != nil {
		return Created{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/user/"+url.PathEscape(username), nil)
	if err != nil {
		return Created{}, err
	}
	req.Header.Set("Authorization", "Bearer "+adm)
	resp, err := c.http.Do(req)
	if err != nil {
		return Created{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Created{}, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return Created{}, fmt.Errorf("marzban GET /api/user/%s: %s", username, resp.Status)
	}
	return decodeCreated(resp.Body)
}

func decodeCreated(r io.Reader) (Created, error) {
	var j struct {
		Username string `json:"username"`
		Token    string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&j); err != nil {
		return Created{}, fmt.Errorf("marzban: decode user: %w", err)
	}
	if j.Token == "" {
		return Created{}, fmt.Errorf("marzban: user %q has no subscription token in response", j.Username)
	}
	return Created{Username: j.Username, Token: j.Token}, nil
}
