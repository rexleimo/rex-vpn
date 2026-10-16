package webkit

import (
	"sync"
	"time"
)

// RateLimiter 滑动窗口限流: 每 key 每 window 最多 limit 次(原 user-center 手撸 10/min 泛化)。
type RateLimiter struct {
	Limit  int
	Window time.Duration

	mu   sync.Mutex
	hits map[string][]time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{Limit: limit, Window: window, hits: map[string][]time.Time{}}
}

// Allow 记录一次并返回是否放行。
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	var keeps []time.Time
	for _, t := range rl.hits[key] {
		if now.Sub(t) < rl.Window {
			keeps = append(keeps, t)
		}
	}
	keeps = append(keeps, now)
	rl.hits[key] = keeps
	return len(keeps) <= rl.Limit
}
