// Package clock abstracts the current time so time-dependent logic (QR token
// expiry, JWT lifetimes, rate limiting) can be driven deterministically in tests.
package clock

import (
	"sync"
	"time"
)

// Clock reports the current time.
type Clock interface {
	Now() time.Time
}

// Real is the production clock backed by time.Now.
type Real struct{}

func (Real) Now() time.Time { return time.Now() }

// Fake is a manually-advanced clock for tests. The zero value starts at the
// zero time; use NewFake to pick a starting point. Safe for concurrent use.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

// NewFake returns a Fake clock set to t.
func NewFake(t time.Time) *Fake { return &Fake{now: t} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance moves the clock forward by d.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

// Set moves the clock to t.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	f.now = t
	f.mu.Unlock()
}
