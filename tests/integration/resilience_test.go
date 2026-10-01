//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"fictin/internal/app"
	"fictin/internal/auth"
	"fictin/internal/models"
	"fictin/internal/testutil"
)

// When the database is unreachable, liveness stays up, readiness reports it,
// and API calls fail with a generic 500 that leaks no internals.
func TestResilience_DatabaseDown(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	admin := srv.AsAdmin(t) // created while the database is still up

	srv.Pool.Close()

	srv.Guest().Get(t, "/healthz").RequireStatus(t, http.StatusOK)
	srv.Guest().Get(t, "/readyz").RequireError(t, http.StatusServiceUnavailable, "DB_UNAVAILABLE")

	for _, path := range []string{"/api/v1/admin/users", "/api/v1/admin/subjects", "/api/v1/admin/attendance"} {
		t.Run(path, func(t *testing.T) {
			res := admin.Get(t, path)
			e := res.RequireError(t, http.StatusInternalServerError, "INTERNAL")
			assert.Equal(t, "an internal error occurred", e.Message)
			assert.NotContains(t, string(res.Body), "pool")
		})
	}
	srv.Guest().Post(t, "/api/v1/auth/login", map[string]string{"email": "a@example.test", "password": "x"}).
		RequireError(t, http.StatusInternalServerError, "INTERNAL")
}

// Many simultaneous scans by the same student (double taps, retries) record
// attendance exactly once; every request gets a 200.
func TestResilience_ConcurrentScansMarkOnce(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	room := srv.Factory.Classroom(t)
	stud := srv.ClientFor(t, room.Student)
	tok := srv.Factory.QRToken(t, room.Session.ID, srv.Clock.Now(), time.Minute)
	body, err := json.Marshal(scanBody(room.Session.ID, tok.Token))
	require.NoError(t, err)

	const n = 10
	type result struct {
		status int
		body   []byte
		err    error
	}
	results := make([]result, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all requests together
			req, err := http.NewRequest(http.MethodPost, srv.URL+scanPath, bytes.NewReader(body))
			if err != nil {
				results[i].err = err
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+stud.Token)
			res, err := srv.Client().Do(req)
			if err != nil {
				results[i].err = err
				return
			}
			defer res.Body.Close()
			results[i].status = res.StatusCode
			results[i].body, results[i].err = io.ReadAll(res.Body)
		}()
	}
	close(start)
	wg.Wait()

	counts := map[string]int{}
	for _, r := range results {
		require.NoError(t, r.err)
		require.Equal(t, http.StatusOK, r.status, "body: %s", r.body)
		var env struct {
			Data struct {
				Status string `json:"status"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(r.body, &env))
		counts[env.Data.Status]++
	}
	assert.Equal(t, map[string]int{"marked": 1, "already_marked": n - 1}, counts)

	var rows int
	require.NoError(t, srv.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM attendance_records WHERE session_id = $1`, room.Session.ID).Scan(&rows))
	assert.Equal(t, 1, rows)
}

// Startup (app.Bootstrap): the development admin is seeded once, and QR
// rotation resumes for sessions that were live when the server stopped.
func TestResilience_BootstrapSeedsAdminAndResumesLiveSessions(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	ctx := context.Background()
	prof := srv.Factory.User(t, models.RoleProfessor)
	live := srv.Factory.LiveSession(t, srv.Factory.Subject(t, prof.ID).ID)
	ended := srv.Factory.EndedSession(t, srv.Factory.Subject(t, prof.ID).ID)

	cfg := *srv.Config
	cfg.AppEnv = "development"
	cfg.AdminSeedEmail = " Seed.Admin@Example.test "
	cfg.AdminSeedPassword = "SeedPass123!"

	// A second app on the same database plays the role of the restarted server.
	restarted := app.New(&cfg, srv.Pool, app.Options{Log: testutil.Logger()})
	t.Cleanup(restarted.Close)
	restarted.Bootstrap(ctx, &cfg, testutil.Logger())
	restarted.Bootstrap(ctx, &cfg, testutil.Logger()) // idempotent

	seeded, err := srv.App.Store.Users.GetByEmail(ctx, "seed.admin@example.test")
	require.NoError(t, err)
	assert.Equal(t, models.RoleAdmin, seeded.Role)
	assert.False(t, seeded.MustChangePassword)
	assert.True(t, auth.NewHasher(4).Compare(seeded.PasswordHash, "SeedPass123!"))

	var admins int
	require.NoError(t, srv.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = 'admin'`).Scan(&admins))
	assert.Equal(t, 1, admins, "seeding runs only when no admin exists")

	assert.True(t, restarted.QR.IsRunning(live.ID), "rotation resumes for live sessions")
	assert.False(t, restarted.QR.IsRunning(ended.ID))

	t.Run("outside development nothing is seeded", func(t *testing.T) {
		other := testutil.NewServer(t)
		prodCfg := *other.Config
		prodCfg.AppEnv = "production"
		prodCfg.AdminSeedEmail = "prod@example.test"
		prodCfg.AdminSeedPassword = "SeedPass123!"
		a := app.New(&prodCfg, other.Pool, app.Options{Log: testutil.Logger()})
		t.Cleanup(a.Close)
		a.Bootstrap(ctx, &prodCfg, testutil.Logger())

		_, err := other.App.Store.Users.GetByEmail(ctx, "prod@example.test")
		assert.Error(t, err)
	})
}

// The background cleanup job purges QR tokens older than 3h and expired
// refresh tokens, keeps everything else, and exits when its context ends
// (graceful shutdown).
func TestResilience_CleanupJobPurgesStaleTokensAndStops(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	ctx := context.Background()
	room := srv.Factory.Classroom(t)
	now := time.Now().UTC()

	stale := srv.Factory.QRToken(t, room.Session.ID, now.Add(-4*time.Hour), 1500*time.Millisecond)
	fresh := srv.Factory.QRToken(t, room.Session.ID, now, 1500*time.Millisecond)
	require.NoError(t, srv.App.Store.Refresh.Create(ctx, room.Student.ID, auth.HashRefreshToken("expired"), now.Add(-time.Hour)))
	require.NoError(t, srv.App.Store.Refresh.Create(ctx, room.Student.ID, auth.HashRefreshToken("valid"), now.Add(time.Hour)))

	tokenExists := func(token string) bool {
		var ok bool
		require.NoError(t, srv.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qr_tokens WHERE token = $1)`, token).Scan(&ok))
		return ok
	}
	refreshCount := func() int {
		var n int
		require.NoError(t, srv.Pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens`).Scan(&n))
		return n
	}

	jobCtx, stop := context.WithCancel(ctx)
	done := app.StartCleanup(jobCtx, srv.App.Store, testutil.Logger())

	// The first purge runs immediately on start.
	require.Eventually(t, func() bool { return !tokenExists(stale.Token) && refreshCount() == 1 },
		5*time.Second, 10*time.Millisecond)
	assert.True(t, tokenExists(fresh.Token))
	_, err := srv.App.Store.Refresh.GetActiveByHash(ctx, auth.HashRefreshToken("valid"))
	assert.NoError(t, err, "unexpired refresh tokens are kept")

	stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup goroutine did not exit after its context was cancelled")
	}
}
