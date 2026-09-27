// Package app wires configuration, the database pool, services and the HTTP
// router into a runnable application. cmd/api uses it for production and
// internal/testutil uses it to build the exact same handler for tests.
package app

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"fictin/internal/auth"
	"fictin/internal/clock"
	"fictin/internal/config"
	"fictin/internal/handlers"
	"fictin/internal/middleware"
	"fictin/internal/repository"
	"fictin/internal/router"
	"fictin/internal/service"
)

// App is a fully wired application.
type App struct {
	Handler http.Handler
	Store   *repository.Store
	QR      *service.QRManager
	Hasher  *auth.Hasher
	Tokens  *auth.TokenManager

	scanLimiter *middleware.PerUserRateLimiter
}

// Options overrides defaults; the zero value is production behavior.
type Options struct {
	Clock clock.Clock  // default clock.Real{}
	Log   *slog.Logger // default slog.Default()
}

// New builds the application around an open pool. Call Close when done.
func New(cfg *config.Config, pool *pgxpool.Pool, opts Options) *App {
	clk := opts.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	store := repository.NewStore(pool)

	hasher := auth.NewHasher(cfg.BcryptCost)
	tokens := auth.NewTokenManager(cfg.JWTAccessSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL, auth.WithClock(clk))
	authMW := auth.NewMiddleware(tokens)

	qrManager := service.NewQRManager(store.QRTokens, store.Sessions, clk, cfg.QRTokenTTL, cfg.QRRotationInterval, log)

	authSvc := service.NewAuthService(store, hasher, tokens)
	subjectSvc := service.NewSubjectService(store)
	sessionSvc := service.NewSessionService(store, subjectSvc, qrManager)
	attendanceSvc := service.NewAttendanceService(store, clk, log)
	adminSvc := service.NewAdminService(store)
	studentSvc := service.NewStudentService(store)

	scanLimiter := middleware.NewPerUserRateLimiter(cfg.ScanRateLimitPerSec, cfg.ScanRateLimitBurst, clk)

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

	return &App{
		Handler:     h,
		Store:       store,
		QR:          qrManager,
		Hasher:      hasher,
		Tokens:      tokens,
		scanLimiter: scanLimiter,
	}
}

// Bootstrap runs startup tasks: seeding the dev admin and resuming QR
// rotation for sessions still marked live.
func (a *App) Bootstrap(ctx context.Context, cfg *config.Config, log *slog.Logger) {
	if cfg.IsDevelopment() {
		if err := service.SeedAdmin(ctx, a.Store, a.Hasher, cfg.AdminSeedEmail, cfg.AdminSeedPassword); err != nil {
			log.Warn("admin seed skipped", "err", err)
		}
	}
	if err := a.QR.ResumeLiveSessions(ctx); err != nil {
		log.Warn("could not resume live sessions", "err", err)
	}
}

// Close stops background goroutines (QR rotation, rate-limiter cleanup). The
// pool is owned by the caller.
func (a *App) Close() {
	a.QR.StopAll()
	a.scanLimiter.Close()
}
