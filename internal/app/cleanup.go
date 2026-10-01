package app

import (
	"context"
	"log/slog"
	"time"

	"fictin/internal/repository"
)

// StartCleanup purges stale qr_tokens (older than 3h) and expired
// refresh_tokens immediately and then every 30 minutes, until ctx is
// cancelled. The returned channel closes when the goroutine has exited.
func StartCleanup(ctx context.Context, store *repository.Store, log *slog.Logger) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(30 * time.Minute)
		defer t.Stop()
		purge := func() {
			c, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if n, err := store.QRTokens.DeleteOlderThan(c, time.Now().UTC().Add(-3*time.Hour)); err != nil {
				log.Warn("qr token cleanup failed", "err", err)
			} else if n > 0 {
				log.Info("purged stale qr tokens", "count", n)
			}
			if n, err := store.Refresh.DeleteExpired(c, time.Now().UTC()); err != nil {
				log.Warn("refresh token cleanup failed", "err", err)
			} else if n > 0 {
				log.Info("purged expired refresh tokens", "count", n)
			}
		}
		purge()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				purge()
			}
		}
	}()
	return done
}
