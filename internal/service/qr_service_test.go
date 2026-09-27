package service

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"fictin/internal/apperr"
	"fictin/internal/clock"
	"fictin/internal/models"
)

var (
	t0         = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	discardLog = slog.New(slog.NewTextHandler(io.Discard, nil))
)

func TestCheckScanToken(t *testing.T) {
	session := uuid.New()
	tok := &models.QRToken{SessionID: session, IssuedAt: t0, ExpiresAt: t0.Add(1500 * time.Millisecond)}

	tests := []struct {
		name      string
		tok       *models.QRToken
		sessionID uuid.UUID
		now       time.Time
		want      error
	}{
		{"valid right after issue", tok, session, t0, nil},
		{"valid exactly at expiry", tok, session, t0.Add(1500 * time.Millisecond), nil},
		{"expired one millisecond later", tok, session, t0.Add(1501 * time.Millisecond), apperr.TokenExpired},
		{"expired long ago", tok, session, t0.Add(time.Hour), apperr.TokenExpired},
		{"token from another session is invalid", tok, uuid.New(), t0, apperr.TokenInvalid},
		{"another session wins over expiry", tok, uuid.New(), t0.Add(time.Hour), apperr.TokenInvalid},
		{"missing token is invalid", nil, session, t0, apperr.TokenInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkScanToken(tt.tok, tt.sessionID, tt.now)
			if tt.want == nil {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, tt.want)
			}
		})
	}
}

func TestGenerateToken(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		tok, err := generateToken()
		require.NoError(t, err)

		raw, err := base64.RawURLEncoding.DecodeString(tok)
		require.NoError(t, err, "token must be URL-safe base64 without padding")
		assert.Len(t, raw, 32, "32 random bytes")
		assert.LessOrEqual(t, len(tok), 128, "fits qr_tokens.token VARCHAR(128)")
		assert.False(t, seen[tok], "tokens must not repeat")
		seen[tok] = true
	}
}

// fakeTokenStore records inserted tokens; it can be told to fail.
type fakeTokenStore struct {
	mu     sync.Mutex
	rows   []models.QRToken
	failOn error
}

func (f *fakeTokenStore) Insert(_ context.Context, sessionID uuid.UUID, token string, issuedAt, expiresAt time.Time) (*models.QRToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn != nil {
		return nil, f.failOn
	}
	row := models.QRToken{ID: uuid.New(), SessionID: sessionID, Token: token, IssuedAt: issuedAt, ExpiresAt: expiresAt}
	f.rows = append(f.rows, row)
	return &row, nil
}

func (f *fakeTokenStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

type fakeLiveSessions []models.ClassSession

func (f fakeLiveSessions) ListLive(context.Context) ([]models.ClassSession, error) { return f, nil }

// newTestManager returns a manager whose ticker never fires during a test
// (1h interval): only the immediate first rotation runs, and tests drive
// further rotations by calling rotate directly. No sleeps needed.
func newTestManager(store *fakeTokenStore, clk clock.Clock) *QRManager {
	return NewQRManager(store, fakeLiveSessions{}, clk, 1500*time.Millisecond, time.Hour, discardLog)
}

func waitForToken(t *testing.T, m *QRManager, sessionID uuid.UUID) TokenMessage {
	t.Helper()
	var msg TokenMessage
	require.Eventually(t, func() bool {
		var ok bool
		msg, ok = m.Current(sessionID)
		return ok
	}, 5*time.Second, time.Millisecond, "first token was never minted")
	return msg
}

func TestQRManagerMintsTokenImmediatelyWithInjectedClock(t *testing.T) {
	store := &fakeTokenStore{}
	clk := clock.NewFake(t0)
	m := newTestManager(store, clk)
	t.Cleanup(m.StopAll)

	session := uuid.New()
	m.Start(session)
	msg := waitForToken(t, m, session)

	assert.Equal(t, session.String(), msg.SessionID)
	assert.Equal(t, t0, msg.IssuedAt)
	assert.Equal(t, t0.Add(1500*time.Millisecond), msg.ExpiresAt)
	assert.NotEmpty(t, msg.Token)
	assert.True(t, m.IsRunning(session))
}

func TestQRManagerRotationBroadcastsToSubscribers(t *testing.T) {
	store := &fakeTokenStore{}
	clk := clock.NewFake(t0)
	m := newTestManager(store, clk)
	t.Cleanup(m.StopAll)

	session := uuid.New()
	m.Start(session)
	first := waitForToken(t, m, session)

	ch, unsubscribe, ok := m.Subscribe(session)
	require.True(t, ok)
	t.Cleanup(unsubscribe)
	assert.Equal(t, first, <-ch, "a new subscriber immediately receives the current token")

	clk.Advance(time.Second)
	m.mu.Lock()
	r := m.runners[session]
	m.mu.Unlock()
	m.rotate(context.Background(), r)

	second := <-ch
	assert.NotEqual(t, first.Token, second.Token)
	assert.Equal(t, t0.Add(time.Second), second.IssuedAt)
	assert.Equal(t, t0.Add(2500*time.Millisecond), second.ExpiresAt)
	current, _ := m.Current(session)
	assert.Equal(t, second, current)
	assert.Equal(t, 2, store.count())
}

func TestQRManagerStopClosesSubscribers(t *testing.T) {
	m := newTestManager(&fakeTokenStore{}, clock.NewFake(t0))
	session := uuid.New()
	m.Start(session)
	waitForToken(t, m, session)

	ch, unsubscribe, ok := m.Subscribe(session)
	require.True(t, ok)
	<-ch // current token

	m.Stop(session)
	_, open := <-ch
	assert.False(t, open, "subscriber channel is closed when the session stops")
	unsubscribe() // safe after stop

	assert.False(t, m.IsRunning(session))
	_, ok = m.Current(session)
	assert.False(t, ok)
	_, _, ok = m.Subscribe(session)
	assert.False(t, ok, "cannot subscribe to a stopped session")
	m.Stop(session) // idempotent
}

func TestQRManagerStartIsIdempotent(t *testing.T) {
	store := &fakeTokenStore{}
	m := newTestManager(store, clock.NewFake(t0))
	t.Cleanup(m.StopAll)
	session := uuid.New()

	m.Start(session)
	waitForToken(t, m, session)
	m.Start(session)

	m.mu.Lock()
	defer m.mu.Unlock()
	assert.Len(t, m.runners, 1)
}

func TestQRManagerStoreFailureDoesNotPublish(t *testing.T) {
	store := &fakeTokenStore{failOn: errors.New("db down")}
	m := newTestManager(store, clock.NewFake(t0))
	t.Cleanup(m.StopAll)
	session := uuid.New()
	m.Start(session)

	m.mu.Lock()
	r := m.runners[session]
	m.mu.Unlock()
	m.rotate(context.Background(), r) // deterministic extra attempt

	_, ok := m.Current(session)
	assert.False(t, ok, "a token that was not persisted must never be shown")
}

func TestQRManagerResumeLiveSessions(t *testing.T) {
	live := fakeLiveSessions{{ID: uuid.New()}, {ID: uuid.New()}}
	m := NewQRManager(&fakeTokenStore{}, live, clock.NewFake(t0), time.Second, time.Hour, discardLog)
	t.Cleanup(m.StopAll)

	require.NoError(t, m.ResumeLiveSessions(context.Background()))
	for _, s := range live {
		assert.True(t, m.IsRunning(s.ID))
	}
}

func TestTokenPrefixNeverLogsFullToken(t *testing.T) {
	tests := map[string]string{
		"abcdefghijklmnop": "abcdef…",
		"abcdef":           "******",
		"":                 "******",
	}
	for in, want := range tests {
		assert.Equal(t, want, tokenPrefix(in))
	}
}
