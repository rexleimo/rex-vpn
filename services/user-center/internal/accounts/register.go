// register.go: 进程内代理 authkit 自助注册端点(POST {APIBase}/register)。
// 仅在 UC_REGISTRATION=open 时被路由调用(NativeUserMode=open 才挂载成功)。
// 注册即登录语义(authflow.RegisterSignedIn): 成功后同 Login 一样种 SSR 会话。
// 无邮件通道下 Verification=none, 注册即激活; 密码策略由 authkit 内部校验。
package accounts

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Register 用邮箱+密码创建账号并返回登录态。username 必填(authkit 契约),
// 由邮箱本地部分清洗生成; 重名冲突由 authkit 报错, 重试一次加随机后缀。
func (s *Service) Register(ctx context.Context, email, password, clientIP string) (Auth, error) {
	username := usernameFromEmail(email)
	a, err := s.registerOnce(ctx, email, username, password, clientIP)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "username") {
		a, err = s.registerOnce(ctx, email, username+randomSuffix(), password, clientIP)
	}
	return a, err
}

func (s *Service) registerOnce(ctx context.Context, email, username, password, clientIP string) (Auth, error) {
	body, err := json.Marshal(map[string]string{"identifier": email, "username": username, "password": password})
	if err != nil {
		return Auth{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Client.APIBase()+"/register", bytes.NewReader(body))
	if err != nil {
		return Auth{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if clientIP != "" {
		req.RemoteAddr = clientIP + ":0"
	}
	rec := &respCapture{hdr: make(map[string][]string)}
	s.Client.Handler().ServeHTTP(rec, req)

	if rec.code != http.StatusOK {
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
		return Auth{}, fmt.Errorf("register: %s", msg)
	}
	// 与 Login 同构的 AuthResult; 202(待验证)在此部署不会出现(Verification=none),
	// 若上游策略变化导致 status 非 complete, 明确报错而非静默半注册。
	var res struct {
		Status string `json:"status"`
		User   *struct {
			ID       string  `json:"id"`
			Email    *string `json:"email"`
			Username string  `json:"username"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.buf.Bytes(), &res); err != nil {
		return Auth{}, fmt.Errorf("register decode: %w", err)
	}
	if res.Status != "complete" || res.User == nil {
		return Auth{}, fmt.Errorf("register: 状态 %s 需要额外验证步骤(当前部署未配置邮件通道)", res.Status)
	}
	a := Auth{UserID: res.User.ID, Username: res.User.Username}
	if res.User.Email != nil {
		a.Email = *res.User.Email
	}
	return a, nil
}

// randomSuffix 注册重名重试时附加, 避免与已有用户名碰撞。
func randomSuffix() string {
	b := make([]byte, 3)
	_, _ = crand.Read(b)
	return "_" + hex.EncodeToString(b)
}
