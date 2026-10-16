package webkit

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"net/http"
	"sync"
	"time"
)

// Session 是内存态 cookie 会话(服务端持有随机 sid → 业务值)。
// 重启失效对管理面板/单用户门户均可接受(与原两服务手撸实现语义一致)。
type Session struct {
	Name   string        // cookie 名(如 uc_sess / wm_admin)
	TTL    time.Duration // cookie Max-Age
	Secure bool          // 线上 HTTPS 置 true

	mu     sync.Mutex
	values map[string]string // sid -> 业务值(admin 用空串; user-center 存 sub token)
}

func NewSession(name string, ttl time.Duration, secure bool) *Session {
	return &Session{Name: name, TTL: ttl, Secure: secure, values: map[string]string{}}
}

// Issue 发放随机 sid cookie, 返回 sid。
func (s *Session) Issue(w http.ResponseWriter, value string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	sid := hex.EncodeToString(b)
	s.mu.Lock()
	s.values[sid] = value
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     s.Name,
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.TTL.Seconds()),
		Secure:   s.Secure,
	})
	return sid
}

// Value 从请求 cookie 查会话值。
func (s *Session) Value(r *http.Request) (value string, sid string, ok bool) {
	c, err := r.Cookie(s.Name)
	if err != nil || c.Value == "" {
		return "", "", false
	}
	s.mu.Lock()
	value, ok = s.values[c.Value]
	s.mu.Unlock()
	return value, c.Value, ok
}

// Revoke 删除会话并清 cookie(需已登录态拿 sid; sid 空则只清 cookie)。
func (s *Session) Revoke(w http.ResponseWriter, r *http.Request) {
	if _, sid, ok := s.Value(r); ok {
		s.mu.Lock()
		delete(s.values, sid)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: s.Name, Value: "", MaxAge: -1, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

// ClearIfEmptyRevoke 供"先换新会话再作废旧的"场景: 直接作废旧 sid。
func (s *Session) DropBySID(sid string) {
	s.mu.Lock()
	delete(s.values, sid)
	s.mu.Unlock()
}

// PasswordOrGenerated 口令为空时随机生成并打日志(开发态语义, 原 ingest NewSession)。
func PasswordOrGenerated(envPW, envKey string) string {
	if envPW != "" {
		return envPW
	}
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	pw := "dev-" + hex.EncodeToString(b)
	log.Printf("webkit: %s 未设置, 本次运行临时口令=%s (重启失效)", envKey, pw)
	return pw
}

// ConstantTimeEqual 恒时比较(登录口令校验复用)。
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
