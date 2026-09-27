//go:build integration

// Package integration exercises the real router, middleware, services and a
// real Postgres database over HTTP, the way the frontend does.
//
// Run with: go test -tags=integration ./tests/integration/
package integration

import (
	"os"
	"testing"

	"fictin/internal/testutil"
)

// TestMain starts one Postgres container for this package; each test then
// gets its own database via testutil.NewServer / testutil.NewDB.
func TestMain(m *testing.M) { os.Exit(testutil.RunWithDatabase(m)) }
