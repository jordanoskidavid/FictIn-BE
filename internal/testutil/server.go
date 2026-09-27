package testutil

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"fictin/internal/app"
	"fictin/internal/clock"
	"fictin/internal/config"
)

// Config returns the configuration from .env.test at the repository root.
// The process environment is deliberately ignored so tests are reproducible.
func Config(t testing.TB) *config.Config {
	t.Helper()
	env, err := godotenv.Read(filepath.Join(RepoRoot(t), ".env.test"))
	if err != nil {
		t.Fatalf("read .env.test: %v", err)
	}
	cfg, err := config.Parse(func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	})
	if err != nil {
		t.Fatalf("parse .env.test: %v", err)
	}
	return cfg
}

// RepoRoot walks up from the working directory to the directory holding go.mod.
func RepoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the working directory")
		}
		dir = parent
	}
}

// Server is the real application (router, middleware, services, repositories)
// served by httptest against a fresh per-test database.
type Server struct {
	*httptest.Server
	App     *app.App
	Pool    *pgxpool.Pool
	Config  *config.Config
	Clock   *clock.Fake
	Factory *Factory
}

// ServerOption tweaks configuration before the app is built.
type ServerOption func(*config.Config)

// WithConfig lets a test adjust configuration (timeouts, rate limits, ...).
func WithConfig(fn func(*config.Config)) ServerOption { return ServerOption(fn) }

// NewServer builds the app on a fresh database. Everything is torn down when
// the test ends. The fake clock starts at the real current time because the
// database also stamps rows with its own now().
func NewServer(t testing.TB, opts ...ServerOption) *Server {
	t.Helper()
	pool, dsn := NewDB(t)

	cfg := Config(t)
	cfg.DatabaseURL = dsn
	for _, o := range opts {
		o(cfg)
	}

	clk := clock.NewFake(time.Now().UTC())
	a := app.New(cfg, pool, app.Options{Clock: clk, Log: Logger()})

	srv := httptest.NewServer(a.Handler)
	t.Cleanup(srv.Close)
	t.Cleanup(a.Close) // runs first: ends QR rotation and WebSocket streams

	return &Server{
		Server:  srv,
		App:     a,
		Pool:    pool,
		Config:  cfg,
		Clock:   clk,
		Factory: NewFactory(a.Store, a.Hasher),
	}
}

// Logger discards logs unless TEST_LOG=1, keeping test output readable.
func Logger() *slog.Logger {
	if os.Getenv("TEST_LOG") == "1" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
