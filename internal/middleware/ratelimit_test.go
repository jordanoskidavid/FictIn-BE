package middleware

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPerUserRateLimiter(t *testing.T) {
	rl := NewPerUserRateLimiter(5, 3)

	// Burst of 3 allowed immediately.
	assert.True(t, rl.allow("user-1"))
	assert.True(t, rl.allow("user-1"))
	assert.True(t, rl.allow("user-1"))
	assert.False(t, rl.allow("user-1"), "4th request in burst should be denied")

	// A different key has its own bucket.
	assert.True(t, rl.allow("user-2"))

	// After refill time, user-1 is allowed again.
	time.Sleep(250 * time.Millisecond) // 5/s => ~1.25 tokens
	assert.True(t, rl.allow("user-1"))
}
