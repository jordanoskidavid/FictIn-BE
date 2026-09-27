package service_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"fictin/internal/auth"
	"fictin/internal/clock"
	"fictin/internal/database"
	"fictin/internal/models"
	"fictin/internal/repository"
	"fictin/internal/service"
)

// setup connects to TEST_DATABASE_URL, migrates, and truncates all data.
// The whole package's integration tests are skipped when the var is unset.
func setup(t *testing.T) (*repository.Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration tests")
	}
	require.NoError(t, database.RunMigrations(dsn))

	pool, err := database.Connect(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	_, err = pool.Exec(context.Background(), `
		TRUNCATE attendance_records, qr_tokens, class_sessions, class_schedules,
		enrollments, subjects, refresh_tokens, users RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	return repository.NewStore(pool), pool
}

func TestAttendanceHappyPath(t *testing.T) {
	store, _ := setup(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	hasher := auth.NewHasher(4)
	tokens := auth.NewTokenManager("test", 15*time.Minute, time.Hour)
	authSvc := service.NewAuthService(store, hasher, tokens)
	subjectSvc := service.NewSubjectService(store)
	qr := service.NewQRManager(store.QRTokens, store.Sessions, clock.Real{}, 1500*time.Millisecond, 1*time.Second, log)
	sessionSvc := service.NewSessionService(store, subjectSvc, qr)
	attendanceSvc := service.NewAttendanceService(store, clock.Real{}, log)

	// --- users ---
	prof, err := authSvc.Register(ctx, service.RegisterInput{
		FullName: "Prof", Email: "p@x.com", Password: "password1", Role: models.RoleProfessor,
	})
	require.NoError(t, err)

	code := "S1"
	stud, err := authSvc.Register(ctx, service.RegisterInput{
		FullName: "Stud", Email: "s@x.com", Password: "password1", Role: models.RoleStudent, StudentCode: &code,
	})
	require.NoError(t, err)

	// --- subject + enrollment ---
	subj, err := subjectSvc.Create(ctx, prof.ID, "Databases", "cs101")
	require.NoError(t, err)
	require.Equal(t, "CS101", subj.Code)

	_, err = subjectSvc.Enroll(ctx, subj.ID, prof.ID, &stud.ID, nil)
	require.NoError(t, err)

	// --- session start ---
	sess, err := sessionSvc.Start(ctx, subj.ID, prof.ID, nil)
	require.NoError(t, err)
	require.Equal(t, models.SessionLive, sess.Status)
	t.Cleanup(func() { qr.StopAll() })

	// one-live-session-per-subject
	_, err = sessionSvc.Start(ctx, subj.ID, prof.ID, nil)
	require.Error(t, err)

	// --- issue a token directly (deterministic, no sleep) ---
	now := time.Now().UTC()
	tok, err := store.QRTokens.Insert(ctx, sess.ID, "tok-"+uuid.NewString(), now, now.Add(2*time.Second))
	require.NoError(t, err)

	// --- scan ---
	res, err := attendanceSvc.Scan(ctx, stud.ID, sess.ID, tok.Token)
	require.NoError(t, err)
	require.Equal(t, "marked", res.Status)

	// idempotent
	res, err = attendanceSvc.Scan(ctx, stud.ID, sess.ID, tok.Token)
	require.NoError(t, err)
	require.Equal(t, "already_marked", res.Status)

	// --- stop ---
	_, err = sessionSvc.Stop(ctx, sess.ID, prof.ID)
	require.NoError(t, err)

	_, err = attendanceSvc.Scan(ctx, stud.ID, sess.ID, tok.Token)
	require.ErrorContains(t, err, "SESSION_NOT_LIVE")
}

func TestScanRejections(t *testing.T) {
	store, _ := setup(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	hasher := auth.NewHasher(4)
	tokens := auth.NewTokenManager("test", 15*time.Minute, time.Hour)
	authSvc := service.NewAuthService(store, hasher, tokens)
	subjectSvc := service.NewSubjectService(store)
	qr := service.NewQRManager(store.QRTokens, store.Sessions, clock.Real{}, 1500*time.Millisecond, time.Second, log)
	sessionSvc := service.NewSessionService(store, subjectSvc, qr)
	attendanceSvc := service.NewAttendanceService(store, clock.Real{}, log)
	t.Cleanup(func() { qr.StopAll() })

	prof, err := authSvc.Register(ctx, service.RegisterInput{FullName: "P", Email: "p@x.com", Password: "password1", Role: models.RoleProfessor})
	require.NoError(t, err)
	code := "S1"
	stud, err := authSvc.Register(ctx, service.RegisterInput{FullName: "S", Email: "s@x.com", Password: "password1", Role: models.RoleStudent, StudentCode: &code})
	require.NoError(t, err)
	subj, err := subjectSvc.Create(ctx, prof.ID, "DB", "c1")
	require.NoError(t, err)
	sess, err := sessionSvc.Start(ctx, subj.ID, prof.ID, nil)
	require.NoError(t, err)

	now := time.Now().UTC()
	good, err := store.QRTokens.Insert(ctx, sess.ID, "good", now, now.Add(2*time.Second))
	require.NoError(t, err)
	expired, err := store.QRTokens.Insert(ctx, sess.ID, "expired", now.Add(-10*time.Second), now.Add(-8*time.Second))
	require.NoError(t, err)

	// not enrolled yet
	_, err = attendanceSvc.Scan(ctx, stud.ID, sess.ID, good.Token)
	require.ErrorContains(t, err, "NOT_ENROLLED")

	_, err = subjectSvc.Enroll(ctx, subj.ID, prof.ID, &stud.ID, nil)
	require.NoError(t, err)

	// invalid token
	_, err = attendanceSvc.Scan(ctx, stud.ID, sess.ID, "nope")
	require.ErrorContains(t, err, "TOKEN_INVALID")

	// expired token
	_, err = attendanceSvc.Scan(ctx, stud.ID, sess.ID, expired.Token)
	require.ErrorContains(t, err, "TOKEN_EXPIRED")

	// wrong session
	other, err := subjectSvc.Create(ctx, prof.ID, "DB2", "c2")
	require.NoError(t, err)
	otherSess, err := sessionSvc.Start(ctx, other.ID, prof.ID, nil)
	require.NoError(t, err)
	_, err = attendanceSvc.Scan(ctx, stud.ID, otherSess.ID, good.Token)
	require.ErrorContains(t, err, "TOKEN_INVALID")
}
