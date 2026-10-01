//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/require"

	"fictin/internal/database"
	"fictin/internal/testutil"
	"fictin/migrations"
)

// Every migration can be rolled back and re-applied cleanly, so a bad
// deploy can be reverted with the down scripts.
func TestMigrations_DownAndUpAgain(t *testing.T) {
	t.Parallel()
	pool, dsn := testutil.NewDB(t) // already fully migrated
	pool.Close()                   // migrate uses its own connection

	src, err := iofs.New(migrations.FS, ".")
	require.NoError(t, err)
	m, err := migrate.NewWithSourceInstance("iofs", src, "pgx5://"+strings.TrimPrefix(dsn, "postgres://"))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })

	version, dirty, err := m.Version()
	require.NoError(t, err)
	require.False(t, dirty)
	require.GreaterOrEqual(t, version, uint(2))

	require.NoError(t, m.Down(), "down migrations")
	require.NoError(t, m.Up(), "up migrations after down")
	again, dirty, err := m.Version()
	require.NoError(t, err)
	require.False(t, dirty)
	require.Equal(t, version, again)

	// The re-created schema works.
	check, err := database.Connect(context.Background(), dsn)
	require.NoError(t, err)
	defer check.Close()
	_, err = check.Exec(context.Background(), `SELECT family_id FROM refresh_tokens LIMIT 1`)
	require.NoError(t, err)
}
