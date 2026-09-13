package middleware

import (
	"net/http"
	"sync"
	"time"

	"fictin/internal/apperr"
	"fictin/internal/auth"
	"fictin/internal/httpresponse"
)

// tokenBucket is a simple per-key rate limiter.
type tokenBucket struct {
	tokens float64
	last   time.Time
}

// PerUserRateLimiter limits requests per authenticated user id.
// Intended to guard POST /attendance/scan (spec §11).
type PerUserRateLimiter struct {
	rate  float64 // tokens per second
	burst float64
	mu    sync.Mutex
	users map[string]*tokenBucket
}

func NewPerUserRateLimiter(ratePerSec float64, burst int) *PerUserRateLimiter {
	if ratePerSec <= 0 {
		ratePerSec = 5
	}
	if burst <= 0 {
		burst = int(ratePerSec)
	}
	rl := &PerUserRateLimiter{
		rate:  ratePerSec,
		burst: float64(burst),
		users: make(map[string]*tokenBucket),
	}
	go rl.gc()
	return rl
}

func (rl *PerUserRateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.users[key]
	if !ok {
		rl.users[key] = &tokenBucket{tokens: rl.burst - 1, last: now}
		return true
	}
	elapsed := now.Sub(b.last).Seconds()
	b.tokens = min(rl.burst, b.tokens+elapsed*rl.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (rl *PerUserRateLimiter) gc() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		rl.mu.Lock()
		cutoff := time.Now().Add(-10 * time.Minute)
		for k, b := range rl.users {
			if b.last.Before(cutoff) {
				delete(rl.users, k)
			}
		}
		rl.mu.Unlock()
	}
}

// Middleware enforces the limit; it must run after auth.AuthRequired.
func (rl *PerUserRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.FromContext(r.Context())
		key := r.RemoteAddr
		if ok {
			key = id.UserID.String()
		}
		if !rl.allow(key) {
			httpresponse.FromError(w, r, apperr.RateLimited)
			return
		}
		next.ServeHTTP(w, r)
	})
}
