package middleware

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"fictin/internal/clock"
)

func TestPerUserRateLimiter(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC))
	rl := NewPerUserRateLimiter(5, 3, clk)
	t.Cleanup(rl.Close)

	// Burst of 3 allowed immediately.
	assert.True(t, rl.allow("user-1"))
	assert.True(t, rl.allow("user-1"))
	assert.True(t, rl.allow("user-1"))
	assert.False(t, rl.allow("user-1"), "4th request in burst should be denied")

	// A different key has its own bucket.
	assert.True(t, rl.allow("user-2"))

	// 5/s => 250ms refills ~1.25 tokens.
	clk.Advance(250 * time.Millisecond)
	assert.True(t, rl.allow("user-1"))
	assert.False(t, rl.allow("user-1"), "only one token should have refilled")
}

func TestPerUserRateLimiterPurgesIdleBuckets(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC))
	rl := NewPerUserRateLimiter(5, 1, clk)
	t.Cleanup(rl.Close)

	rl.allow("idle")
	clk.Advance(9 * time.Minute)
	rl.allow("active")
	clk.Advance(2 * time.Minute)
	rl.purgeIdle()

	rl.mu.Lock()
	defer rl.mu.Unlock()
	assert.NotContains(t, rl.users, "idle")
	assert.Contains(t, rl.users, "active")
}
