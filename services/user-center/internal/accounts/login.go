// login.go: 进程内代理 authkit 的密码登录端点(POST {APIBase}/password/login)。
// 限流/口令校验/会话签发都发生在 authkit 内部; 我们只拿结果种 SSR 会话。
// v1 说明: token_set 在服务端即弃(不落浏览器), 登出仅作废 webkit 会话 ——
// authkit 侧会话因 refresh token 从未交付客户端而不可被外部使用(深度撤销见 WI-6)。
package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Auth 是邮箱密码登录成功后的账号信息。
type Auth struct {
	UserID   string
	Email    string
	Username string
}

// Login 校验 identifier(邮箱/用户名)+password, 成功返回账号。
// clientIP 用于 authkit 内部限流分桶。
func (s *Service) Login(ctx context.Context, identifier, password, clientIP string) (Auth, error) {
	body, err := json.Marshal(map[string]string{"identifier": identifier, "password": password})
	if err != nil {
		return Auth{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Client.APIBase()+"/password/login", bytes.NewReader(body))
	if err != nil {
		return Auth{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if clientIP != "" {
		req.RemoteAddr = clientIP + ":0"
	}
	rec := &respCapture{hdr: make(map[string][]string)}
	s.Client.Handler().ServeHTTP(rec, req)

	switch rec.code {
	case http.StatusOK:
	default:
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(rec.buf.Bytes(), &env)
		msg := env.Error.Message
		if msg == "" {
			msg = env.Message
		}
		if msg == "" {
			raw := strings.TrimSpace(rec.buf.String())
			if len(raw) > 80 {
				raw = raw[:80]
			}
			msg = raw
		}
		if msg == "" {
			msg = fmt.Sprintf("http %d", rec.code)
		}
		return Auth{}, fmt.Errorf("login: %s", msg)
	}

	var res struct {
		Status string `json:"status"`
		User   *struct {
			ID       string  `json:"id"`
			Email    *string `json:"email"`
			Username string  `json:"username"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.buf.Bytes(), &res); err != nil {
		return Auth{}, fmt.Errorf("login decode: %w", err)
	}
	if res.Status != "complete" || res.User == nil {
		return Auth{}, fmt.Errorf("login: 状态 %s 需要额外步骤(当前部署不支持, 请联系管理员)", res.Status)
	}
	a := Auth{UserID: res.User.ID, Username: res.User.Username}
	if res.User.Email != nil {
		a.Email = *res.User.Email
	}
	return a, nil
}

type respCapture struct {
	code int
	hdr  map[string][]string
	buf  bytes.Buffer
}

func (r *respCapture) Header() http.Header { return r.hdr }
func (r *respCapture) Write(b []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.buf.Write(b)
}
func (r *respCapture) WriteHeader(c int) { r.code = c }
