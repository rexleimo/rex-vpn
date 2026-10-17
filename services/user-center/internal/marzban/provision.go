// Provisioning: 订阅付款后由系统创建/更新 Marzban 用户。
// 契约(实测本构建 openapi + 源码 models/user.py): POST /api/user(409 已存在→幂等回读),
// PUT /api/user/{username}(部分更新)。proxies/inbounds 为按协议分组字典,
// 由 GET /api/inbounds 推导; MARZBAN_INBOUND_IDS(逗号分隔 tag)作可选白名单。
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

// inboundsByProtocol 拉 GET /api/inbounds(本构建响应按协议分组:
// {"vless":[{tag,protocol,...}]}), 返回协议→tag 列表。
func (c *httpClient) inboundsByProtocol(ctx context.Context) (map[string][]string, error) {
	adm, err := c.adminToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/inbounds", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+adm)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("marzban GET /api/inbounds: %s", resp.Status)
	}
	var grouped map[string][]struct {
		Tag string `json:"tag"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&grouped); err != nil {
		return nil, fmt.Errorf("marzban: decode inbounds: %w", err)
	}
	out := map[string][]string{}
	for proto, list := range grouped {
		for _, ib := range list {
			if ib.Tag != "" {
				out[proto] = append(out[proto], ib.Tag)
			}
		}
	}
	return out, nil
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
	// 本 Marzban 构建契约: proxies/inbounds 都是按协议分组的字典
	// (proxies:{proto:{}}, inbounds:{proto:[tag]}); "all" 会 500。
	ib, err := c.inboundsByProtocol(ctx)
	if err != nil {
		return Created{}, err
	}
	want := inboundIDs() // 可选: 逗号分隔 tag 白名单
	proxies := map[string]any{}
	inbounds := map[string][]string{}
	for proto, tags := range ib {
		kept := tags
		if len(want) > 0 {
			kept = nil
			for _, t := range tags {
				for _, w := range want {
					if w == t {
						kept = append(kept, t)
						break
					}
				}
			}
		}
		if len(kept) == 0 {
			continue
		}
		proxies[proto] = map[string]any{}
		inbounds[proto] = kept
	}
	if len(proxies) == 0 {
		return Created{}, fmt.Errorf("marzban: no matching inbounds for provisioning")
	}
	body["proxies"] = proxies
	body["inbounds"] = inbounds
	resp, err := c.userRequest(ctx, http.MethodPost, "/api/user", body)
	if err != nil {
		return Created{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		// 幂等重试路径: 用户名已在(上次成功一半), 回读 token。
		return c.getUserByUsername(ctx, spec.Username)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return Created{}, fmt.Errorf("marzban POST /api/user: %s", resp.Status)
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
	resp, err := c.userRequest(ctx, http.MethodPut, "/api/user/"+url.PathEscape(username), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("marzban PUT /api/user/%s: %s", username, resp.Status)
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
		SubURL   string `json:"subscription_url"`
	}
	if err := json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&j); err != nil {
		return Created{}, fmt.Errorf("marzban: decode user: %w", err)
	}
	// 本构建无独立 token 字段, 订阅 token 在 subscription_url(/sub/<token>)。
	tok := j.SubURL
	if i := strings.Index(tok, "/sub/"); i >= 0 {
		tok = tok[i+len("/sub/"):]
	} else {
		tok = ""
	}
	if tok == "" {
		return Created{}, fmt.Errorf("marzban: user %q has no subscription token in response", j.Username)
	}
	return Created{Username: j.Username, Token: tok}, nil
}
