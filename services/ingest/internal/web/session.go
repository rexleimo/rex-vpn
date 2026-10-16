package web

import (
	"net/http"
	"time"

	"vpnmaster/webkit"
)

// 会话: webkit 共享套件(ADR-0001)。cookie 值 = 随机 16B sid, 服务端内存态;
// 重启失效所有会话(管理面板非高可用, 可接受)。
const adminCookie = "wm_admin"

type Session struct {
	Password string // env INGEST_ADMIN_PASSWORD
	st       *webkit.Session
}

// NewSession 口令为空时随机生成并打日志(开发态: 重启失效, 用日志一次性查看)。
func NewSession(password string) *Session {
	return &Session{
		Password: webkit.PasswordOrGenerated(password, "INGEST_ADMIN_PASSWORD"),
		st:       webkit.NewSession(adminCookie, 12*time.Hour, false),
	}
}

func (s *Session) LoginOK(plain string) bool {
	return webkit.ConstantTimeEqual(plain, s.Password)
}

// Issue 发放会话 cookie 值。
func (s *Session) Issue(w http.ResponseWriter) {
	s.st.Issue(w, "")
}

// LoggedIn 请求是否带有效会话(供 gin 中间件用)。
func (s *Session) LoggedIn(r *http.Request) bool {
	_, _, ok := s.st.Value(r)
	return ok
}

// Revoke 登出。
func (s *Session) Revoke(w http.ResponseWriter, r *http.Request) {
	s.st.Revoke(w, r)
}
