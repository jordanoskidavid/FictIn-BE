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

	"fictin/internal/auth"
	"fictin/internal/config"
	"fictin/internal/database"
	"fictin/internal/handlers"
	"fictin/internal/middleware"
	"fictin/internal/repository"
	"fictin/internal/router"
	"fictin/internal/service"
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

	store := repository.NewStore(pool)

	// --- shared components ---
	hasher := auth.NewHasher(cfg.BcryptCost)
	tokens := auth.NewTokenManager(cfg.JWTAccessSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)
	authMW := auth.NewMiddleware(tokens)

	qrManager := service.NewQRManager(store, cfg.QRTokenTTL, cfg.QRRotationInterval, log)

	authSvc := service.NewAuthService(store, hasher, tokens)
	subjectSvc := service.NewSubjectService(store)
	sessionSvc := service.NewSessionService(store, subjectSvc, qrManager)
	attendanceSvc := service.NewAttendanceService(store, log)
	adminSvc := service.NewAdminService(store)
	studentSvc := service.NewStudentService(store)

	// --- bootstrap admin (development only) ---
	if cfg.IsDevelopment() {
		if err := service.SeedAdmin(rootCtx, store, hasher, cfg.AdminSeedEmail, cfg.AdminSeedPassword); err != nil {
			log.Warn("admin seed skipped", "err", err)
		}
	}

	// --- resume rotation for sessions still marked live ---
	if err := qrManager.ResumeLiveSessions(rootCtx); err != nil {
		log.Warn("could not resume live sessions", "err", err)
	}

	scanLimiter := middleware.NewPerUserRateLimiter(cfg.ScanRateLimitPerSec, cfg.ScanRateLimitBurst)

	h := router.New(router.Deps{
		Log:         log,
		AuthMW:      authMW,
		ScanLimiter: scanLimiter,
		CORSOrigins: cfg.CORSAllowedOrigins,
		Health:      handlers.NewHealthHandler(pool),
		Auth:        handlers.NewAuthHandler(authSvc),
		Admin:       handlers.NewAdminHandler(adminSvc),
		Subject:     handlers.NewSubjectHandler(subjectSvc),
		Session:     handlers.NewSessionHandler(sessionSvc, attendanceSvc, qrManager, store),
		Attendance:  handlers.NewAttendanceHandler(attendanceSvc, studentSvc),
		WS:          handlers.NewWSHandler(sessionSvc, qrManager, cfg.CORSAllowedOrigins),
	})

	srv := &http.Server{
		Addr:              ":" + cfg.AppPort,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// --- background cleanup ---
	cleanupDone := startCleanup(rootCtx, store, log)

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
	qrManager.StopAll()
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
