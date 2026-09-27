package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fictin/internal/app"
	"fictin/internal/config"
	"fictin/internal/database"
	"fictin/internal/repository"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := newLogger(cfg.IsDevelopment())
	slog.SetDefault(log)

	// --- migrations ---
	if err := database.RunMigrations(cfg.DatabaseURL); err != nil {
		return err
	}
	log.Info("migrations applied")

	// --- db pool ---
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.Connect(rootCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// --- wiring, admin seed (development only), resume live sessions ---
	a := app.New(cfg, pool, app.Options{Log: log})
	a.Bootstrap(rootCtx, cfg, log)

	srv := &http.Server{
		Addr:              ":" + cfg.AppPort,
		Handler:           a.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// --- background cleanup ---
	cleanupDone := startCleanup(rootCtx, a.Store, log)

	// --- serve ---
	serveErr := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", srv.Addr, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		return err
	case <-rootCtx.Done():
		log.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
	}
	a.Close()
	<-cleanupDone
	log.Info("bye")
	return nil
}
func newLogger(dev bool) *slog.Logger {
	level := slog.LevelInfo
	if dev {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

// startCleanup purges stale qr_tokens and expired refresh_tokens periodically.
func startCleanup(ctx context.Context, store *repository.Store, log *slog.Logger) <-chan struct{} {
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
