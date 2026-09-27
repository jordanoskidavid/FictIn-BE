package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"fictin/internal/database"
)

// DefaultPostgresImage matches the Postgres major version the app runs on.
// Override with TEST_POSTGRES_IMAGE.
const DefaultPostgresImage = "postgres:18-alpine"

// Every database this package creates starts with this prefix, and it only
// ever drops databases it created itself.
const dbPrefix = "fictin_test_"

// dbServer is the Postgres server shared by all tests in one test binary
// (i.e. one package). It holds a migrated template database that each test
// clones.
type dbServer struct {
	adminURL string // connects to a maintenance database with CREATEDB rights
	template string
}

var shared *dbServer

// RunWithDatabase is meant for TestMain in integration-test packages:
//
//	func TestMain(m *testing.M) { os.Exit(testutil.RunWithDatabase(m)) }
//
// It starts one Postgres container for the package (or uses TEST_POSTGRES_URL,
// see CONTRIBUTING.md), applies migrations to a template database, runs the
// tests, and tears everything down.
func RunWithDatabase(m *testing.M) int {
	ctx := context.Background()

	adminURL, terminate, err := startServer(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer terminate()

	template := dbPrefix + "tpl_" + randomSuffix()
	if err := createTemplate(ctx, adminURL, template); err != nil {
		fmt.Fprintf(os.Stderr, "integration setup: %v\n", err)
		return 1
	}
	defer func() { _ = dropDatabase(context.Background(), adminURL, template) }()

	shared = &dbServer{adminURL: adminURL, template: template}
	return m.Run()
}

// NewDB creates a fresh, fully migrated database for this test (cloned from
// the template, ~tens of ms) and drops it when the test ends. Tests using it
// are completely isolated and may call t.Parallel.
func NewDB(t testing.TB) (pool *pgxpool.Pool, dsn string) {
	t.Helper()
	if shared == nil {
		t.Fatal("testutil.NewDB: no database server; add `func TestMain(m *testing.M) { os.Exit(testutil.RunWithDatabase(m)) }` to this package")
	}
	ctx := context.Background()
	name := dbPrefix + randomSuffix()

	admin, err := pgx.Connect(ctx, shared.adminURL)
	if err != nil {
		t.Fatalf("connect to test server: %v", err)
	}
	_, err = admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s TEMPLATE %s`, pgx.Identifier{name}.Sanitize(), pgx.Identifier{shared.template}.Sanitize()))
	_ = admin.Close(ctx)
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		if err := dropDatabase(context.Background(), shared.adminURL, name); err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
	})

	dsn = withDatabase(shared.adminURL, name)
	pool, err = database.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(pool.Close) // runs before the drop above (cleanups are LIFO)
	return pool, dsn
}

func startServer(ctx context.Context) (adminURL string, terminate func(), err error) {
	if raw := os.Getenv("TEST_POSTGRES_URL"); raw != "" {
		if err := CheckTestPostgresURL(raw); err != nil {
			return "", nil, err
		}
		return raw, func() {}, nil
	}

	image := os.Getenv("TEST_POSTGRES_IMAGE")
	if image == "" {
		image = DefaultPostgresImage
	}

	// testcontainers can panic when no Docker host is found; turn that into
	// the same friendly error.
	defer func() {
		if r := recover(); r != nil {
			err = dockerUnavailable(fmt.Errorf("%v", r))
		}
	}()

	c, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase("postgres"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		if c != nil {
			_ = testcontainers.TerminateContainer(c)
		}
		return "", nil, dockerUnavailable(err)
	}
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return "", nil, err
	}
	return dsn, func() { _ = testcontainers.TerminateContainer(c) }, nil
}

func dockerUnavailable(cause error) error {
	return fmt.Errorf(`
================================================================================
Integration tests need Docker, but a Postgres container could not be started.

  - Windows / macOS: start Docker Desktop and wait until it reports "running".
  - Linux: start the daemon (sudo systemctl start docker) and make sure your
    user can reach it (docker ps should work without sudo).

Check with:  docker info

Alternatively, point TEST_POSTGRES_URL at a dedicated *test* Postgres server
(see CONTRIBUTING.md, "Running integration tests without Docker").

Underlying error: %w
================================================================================`, cause)
}

// CheckTestPostgresURL refuses any TEST_POSTGRES_URL that is not clearly a
// test database: the database name or the user name must contain "test".
// This guards against accidentally pointing the suite (which creates and drops
// databases) at a development or production server.
func CheckTestPostgresURL(raw string) error {
	if !strings.HasPrefix(raw, "postgres://") && !strings.HasPrefix(raw, "postgresql://") {
		return errors.New("TEST_POSTGRES_URL must be a URL (postgres://user:pass@host:port/db?sslmode=disable)")
	}
	cfg, err := pgx.ParseConfig(raw)
	if err != nil {
		return fmt.Errorf("TEST_POSTGRES_URL is not a valid Postgres URL: %w", err)
	}
	if strings.Contains(strings.ToLower(cfg.Database), "test") ||
		strings.Contains(strings.ToLower(cfg.User), "test") {
		return nil
	}
	return fmt.Errorf("refusing to use TEST_POSTGRES_URL (user %q, database %q): "+
		"the user or database name must contain \"test\" so a dev or production "+
		"database is never touched; see CONTRIBUTING.md", cfg.User, cfg.Database)
}

func createTemplate(ctx context.Context, adminURL, name string) error {
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return fmt.Errorf("connect to test server: %w", err)
	}
	_, err = admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize())
	_ = admin.Close(ctx)
	if err != nil {
		return fmt.Errorf("create template database (does the user have CREATEDB?): %w", err)
	}
	// RunMigrations closes its connection, leaving the template free to clone.
	if err := database.RunMigrations(withDatabase(adminURL, name)); err != nil {
		_ = dropDatabase(ctx, adminURL, name)
		return err
	}
	return nil
}

func dropDatabase(ctx context.Context, adminURL, name string) error {
	if !strings.HasPrefix(name, dbPrefix) {
		return errors.New("refusing to drop a database not created by testutil: " + name)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer func() { _ = admin.Close(ctx) }()
	_, err = admin.Exec(ctx, `DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`)
	return err
}

// withDatabase returns dsn with its database name replaced.
func withDatabase(dsn, name string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		panic(fmt.Sprintf("testutil: unparseable dsn: %v", err))
	}
	u.Path = "/" + name
	return u.String()
}

func randomSuffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
