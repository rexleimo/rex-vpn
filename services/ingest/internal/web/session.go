package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"net/http"
	"time"
)

// 会话(MVP): cookie + env 口令, HttpOnly。
// cookie 值 = 随机 16B; 服务端持内存态: 登录时校验 env 口令后发放。
// 重启会失效所有会话(可接受, 管理面板非高可用场景)。

const cookieName = "wm_admin"

type Session struct {
	Password string // env INGEST_ADMIN_PASSWORD
	tokens   map[string]bool
}

// NewSession 口令为空时随机生成并打日志(开发态: 重启失效, 用日志一次性查看)。
func NewSession(password string) *Session {
	if password == "" {
		b := make([]byte, 12)
		_, _ = rand.Read(b)
		password = "dev-" + hex.EncodeToString(b)
		log.Printf("web: INGEST_ADMIN_PASSWORD 未设置, 本次运行临时口令=%s (重启失效)", password)
	}
	return &Session{Password: password, tokens: map[string]bool{}}
}

func (s *Session) LoginOK(plain string) bool {
	return subtle.ConstantTimeCompare([]byte(plain), []byte(s.Password)) == 1
}

// Issue 发放会话 cookie 值。
func (s *Session) Issue(w http.ResponseWriter) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	s.tokens[tok] = true
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((12 * time.Hour).Seconds()),
	})
}

func (s *Session) Valid(tok string) bool {
	if tok == "" {
		return false
	}
	return s.tokens[tok]
}

// Revoke 登出。
func (s *Session) Revoke(w http.ResponseWriter, tok string) {
	delete(s.tokens, tok)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", MaxAge: -1, Path: "/"})
}

// Guard 会话门槛: 未登录 302 → /login (admin 页); JSON API 由调用方判断直接 401。
func (s *Session) Guard(w http.ResponseWriter, r *http.Request, h http.HandlerFunc) {
	tok := ""
	if c, err := r.Cookie(cookieName); err == nil {
		tok = c.Value
	}
	if !s.Valid(tok) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	h(w, r)
}
