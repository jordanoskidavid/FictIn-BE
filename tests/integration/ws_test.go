//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"fictin/internal/testutil"
)

type tokenMessage struct {
	SessionID string    `json:"session_id"`
	Token     string    `json:"token"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// startSession starts a live session over HTTP (which starts QR rotation).
func startSession(t *testing.T, prof *testutil.Client, subjectID uuid.UUID) string {
	t.Helper()
	var s struct {
		ID string `json:"id"`
	}
	prof.Post(t, "/api/v1/sessions/"+subjectID.String()+"/start", nil).RequireStatus(t, http.StatusCreated).Data(t, &s)
	return s.ID
}

func TestWS_BoardReceivesRotatingTokens(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	prof := srv.AsProfessor(t)
	subj := srv.Factory.Subject(t, prof.User.ID)
	stud := srv.AsStudent(t)
	srv.Factory.Enroll(t, subj.ID, stud.User.ID)
	sessionID := startSession(t, prof, subj.ID)

	conn, res, err := srv.DialWS(t, "/ws/sessions/"+sessionID+"/qr", prof.Token)
	require.NoError(t, err, "handshake: %+v", res)

	// The first frame is the current token; then a new one every rotation
	// (QR_ROTATION_INTERVAL_MS=50 in .env.test). Wait with a timeout, no sleeps.
	seen := map[string]bool{}
	var last tokenMessage
	for len(seen) < 3 {
		raw, err := testutil.ReadJSON[map[string]any](t, conn, 5*time.Second)
		require.NoError(t, err)
		assertShape(t, tokenMessageShape, raw)

		b, _ := json.Marshal(raw)
		require.NoError(t, json.Unmarshal(b, &last))
		assert.Equal(t, sessionID, last.SessionID)
		assert.Equal(t, 1500*time.Millisecond, last.ExpiresAt.Sub(last.IssuedAt), "QR_TOKEN_TTL_MS")
		seen[last.Token] = true
	}

	// A token streamed to the board is accepted by the scan endpoint.
	stud.Post(t, scanPath, map[string]string{"session_id": sessionID, "token": last.Token}).RequireStatus(t, http.StatusOK)
}

func TestWS_AuthorizationHeaderAlsoWorks(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	prof := srv.AsProfessor(t)
	sessionID := startSession(t, prof, srv.Factory.Subject(t, prof.User.ID).ID)

	conn, _, err := srv.DialWS(t, "/ws/sessions/"+sessionID+"/qr", "")
	require.Error(t, err, "no credentials must be rejected")
	require.Nil(t, conn)

	conn, res, err := srv.DialWSWithHeader(t, "/ws/sessions/"+sessionID+"/qr", http.Header{"Authorization": {"Bearer " + prof.Token}})
	require.NoError(t, err, "handshake: %+v", res)
	_, err = testutil.ReadJSON[tokenMessage](t, conn, 5*time.Second)
	require.NoError(t, err)
}

func TestWS_RejectsUnauthorizedConnections(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	owner := srv.AsProfessor(t)
	liveID := startSession(t, owner, srv.Factory.Subject(t, owner.User.ID).ID)
	ended := srv.Factory.EndedSession(t, srv.Factory.Subject(t, owner.User.ID).ID)

	tests := []struct {
		name       string
		sessionID  string
		token      string
		wantStatus int
		wantCode   string
	}{
		{"guest", liveID, "", http.StatusUnauthorized, "UNAUTHORIZED"},
		{"invalid token", liveID, "not.a.jwt", http.StatusUnauthorized, "UNAUTHORIZED"},
		{"student", liveID, srv.AsStudent(t).Token, http.StatusForbidden, "FORBIDDEN"},
		{"admin", liveID, srv.AsAdmin(t).Token, http.StatusForbidden, "FORBIDDEN"},
		{"another professor", liveID, srv.AsProfessor(t).Token, http.StatusForbidden, "FORBIDDEN"},
		{"ended session", ended.ID.String(), owner.Token, http.StatusConflict, "SESSION_NOT_LIVE"},
		{"unknown session", uuid.NewString(), owner.Token, http.StatusNotFound, "NOT_FOUND"},
		{"malformed session id", "nope", owner.Token, http.StatusBadRequest, "BAD_REQUEST"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, res, err := srv.DialWS(t, "/ws/sessions/"+tt.sessionID+"/qr", tt.token)
			require.Error(t, err)
			require.Nil(t, conn)
			assert.Equal(t, tt.wantStatus, res.StatusCode)
			body := res.Body
			var env struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(body, &env), "body: %s", body)
			assert.Equal(t, tt.wantCode, env.Error.Code)
		})
	}
}

func TestWS_StoppingSessionClosesBoardNormally(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	prof := srv.AsProfessor(t)
	sessionID := startSession(t, prof, srv.Factory.Subject(t, prof.User.ID).ID)

	conn, _, err := srv.DialWS(t, "/ws/sessions/"+sessionID+"/qr", prof.Token)
	require.NoError(t, err)
	_, err = testutil.ReadJSON[tokenMessage](t, conn, 5*time.Second)
	require.NoError(t, err)

	prof.Post(t, "/api/v1/sessions/"+sessionID+"/stop", nil).RequireStatus(t, http.StatusOK)

	// Drain until the server closes; the frontend relies on close code 1000.
	for {
		_, err = testutil.ReadJSON[tokenMessage](t, conn, 5*time.Second)
		if err != nil {
			break
		}
	}
	assert.Equal(t, websocket.StatusNormalClosure, websocket.CloseStatus(err), "got %v", err)
}
