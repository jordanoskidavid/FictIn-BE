package database

import (
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"fictin/migrations"
)

// RunMigrations applies all pending "up" migrations embedded in the binary.
// It is a no-op when the schema is already current.
func RunMigrations(databaseURL string) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("load embedded migrations: %w", err)
	}

	// migrate's pgx/v5 driver expects the "pgx5://" scheme.
	m, err := migrate.NewWithSourceInstance("iofs", src, toPgx5URL(databaseURL))
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		_ = srcErr
		_ = dbErr
	}()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

func toPgx5URL(databaseURL string) string {
	switch {
	case len(databaseURL) >= 11 && databaseURL[:11] == "postgres://":
		return "pgx5://" + databaseURL[11:]
	case len(databaseURL) >= 13 && databaseURL[:13] == "postgresql://":
		return "pgx5://" + databaseURL[13:]
	default:
		return databaseURL
	}
}
