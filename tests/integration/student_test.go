//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"fictin/internal/config"
	"fictin/internal/models"
	"fictin/internal/testutil"
)

const scanPath = "/api/v1/attendance/scan"

func scanBody(sessionID uuid.UUID, token string) map[string]string {
	return map[string]string{"session_id": sessionID.String(), "token": token}
}

func TestStudent_ScanValidTokenThenAlreadyMarked(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	room := srv.Factory.Classroom(t)
	stud := srv.ClientFor(t, room.Student)
	tok := srv.Factory.QRToken(t, room.Session.ID, srv.Clock.Now(), 1500*time.Millisecond)

	res := stud.Post(t, scanPath, scanBody(room.Session.ID, tok.Token)).RequireStatus(t, http.StatusOK)
	body := res.DataMap(t)
	assert.Equal(t, "marked", body["status"])
	assertShape(t, attendanceRecordShape, body["record"].(map[string]any))
	_, err := time.Parse(time.RFC3339, body["scanned_at"].(string))
	assert.NoError(t, err)

	// Scanning again (same or newer token) is idempotent: 200, not an error.
	var again map[string]any
	stud.Post(t, scanPath, scanBody(room.Session.ID, tok.Token)).RequireStatus(t, http.StatusOK).Data(t, &again)
	assert.Equal(t, map[string]any{"status": "already_marked"}, again)

	var history []map[string]any
	stud.Get(t, "/api/v1/student/attendance").RequireStatus(t, http.StatusOK).Data(t, &history)
	require.Len(t, history, 1)
	assertShape(t, attendanceRowShape, history[0])
	assert.Equal(t, room.Session.ID.String(), history[0]["session_id"])
	assert.Equal(t, room.Subject.Code, history[0]["subject_code"])
}

func TestStudent_ScanRejections(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	room := srv.Factory.Classroom(t)
	stud := srv.ClientFor(t, room.Student)
	now := srv.Clock.Now()

	valid := srv.Factory.QRToken(t, room.Session.ID, now, 1500*time.Millisecond)
	expired := srv.Factory.QRToken(t, room.Session.ID, now.Add(-10*time.Second), 1500*time.Millisecond)

	otherRoom := srv.Factory.Classroom(t)
	otherToken := srv.Factory.QRToken(t, otherRoom.Session.ID, now, time.Minute)

	ended := srv.Factory.EndedSession(t, srv.Factory.Subject(t, room.Professor.ID).ID)
	endedToken := srv.Factory.QRToken(t, ended.ID, now, time.Minute)

	outsider := srv.AsStudent(t)

	tests := []struct {
		name       string
		client     *testutil.Client
		body       any
		wantStatus int
		wantCode   string
	}{
		{"expired token", stud, scanBody(room.Session.ID, expired.Token), http.StatusGone, "TOKEN_EXPIRED"},
		{"unknown token", stud, scanBody(room.Session.ID, "never-issued-token"), http.StatusBadRequest, "TOKEN_INVALID"},
		{"token from another session", stud, scanBody(room.Session.ID, otherToken.Token), http.StatusBadRequest, "TOKEN_INVALID"},
		{"session not live", stud, scanBody(ended.ID, endedToken.Token), http.StatusConflict, "SESSION_NOT_LIVE"},
		{"unknown session", stud, scanBody(uuid.New(), valid.Token), http.StatusNotFound, "NOT_FOUND"},
		{"not enrolled", outsider, scanBody(room.Session.ID, valid.Token), http.StatusForbidden, "NOT_ENROLLED"},
		{"malformed session id", stud, map[string]string{"session_id": "x", "token": valid.Token}, http.StatusUnprocessableEntity, "VALIDATION_ERROR"},
		{"missing token", stud, map[string]string{"session_id": room.Session.ID.String()}, http.StatusUnprocessableEntity, "VALIDATION_ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.client.Post(t, scanPath, tt.body).RequireError(t, tt.wantStatus, tt.wantCode)
		})
	}

	// None of the rejected scans recorded attendance.
	var history []map[string]any
	stud.Get(t, "/api/v1/student/attendance").RequireStatus(t, http.StatusOK).Data(t, &history)
	assert.Empty(t, history)
}

// The token expires ~1.5s after it is issued; the fake clock moves across
// that boundary without sleeping.
func TestStudent_TokenExpiresAfterTTL(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	room := srv.Factory.Classroom(t)
	late := srv.Factory.User(t, models.RoleStudent)
	srv.Factory.Enroll(t, room.Subject.ID, late.ID)
	tok := srv.Factory.QRToken(t, room.Session.ID, srv.Clock.Now(), 1500*time.Millisecond)

	srv.Clock.Advance(1500 * time.Millisecond)
	srv.ClientFor(t, room.Student).Post(t, scanPath, scanBody(room.Session.ID, tok.Token)).RequireStatus(t, http.StatusOK)

	srv.Clock.Advance(time.Millisecond)
	srv.ClientFor(t, late).Post(t, scanPath, scanBody(room.Session.ID, tok.Token)).
		RequireError(t, http.StatusGone, "TOKEN_EXPIRED")
}

// End to end: the professor starts a real session, reads the rotating token
// through the polling endpoint and the student scans it.
func TestStudent_ScanRotatingTokenFromLiveSession(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	prof := srv.AsProfessor(t)
	subj := srv.Factory.Subject(t, prof.User.ID)
	stud := srv.AsStudent(t)
	srv.Factory.Enroll(t, subj.ID, stud.User.ID)

	var sess struct {
		ID uuid.UUID `json:"id"`
	}
	prof.Post(t, "/api/v1/sessions/"+subj.ID.String()+"/start", nil).RequireStatus(t, http.StatusCreated).Data(t, &sess)

	var msg struct {
		Token string `json:"token"`
	}
	require.Eventually(t, func() bool {
		res := prof.Get(t, "/api/v1/sessions/"+sess.ID.String()+"/qr/current")
		if res.Status != http.StatusOK {
			return false
		}
		res.Data(t, &msg)
		return true
	}, 5*time.Second, 10*time.Millisecond)

	stud.Post(t, scanPath, scanBody(sess.ID, msg.Token)).RequireStatus(t, http.StatusOK)

	var rows []map[string]any
	prof.Get(t, "/api/v1/sessions/"+sess.ID.String()+"/attendance").RequireStatus(t, http.StatusOK).Data(t, &rows)
	require.Len(t, rows, 1)
	assertShape(t, attendanceRowShape, rows[0])
	assert.Equal(t, stud.User.ID.String(), rows[0]["student_id"])

	prof.Post(t, "/api/v1/sessions/"+sess.ID.String()+"/stop", nil).RequireStatus(t, http.StatusOK)
	stud.Post(t, scanPath, scanBody(sess.ID, msg.Token)).RequireError(t, http.StatusConflict, "SESSION_NOT_LIVE")
}

func TestStudent_ScanIsRateLimited(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t, testutil.WithConfig(func(c *config.Config) {
		c.ScanRateLimitPerSec = 1
		c.ScanRateLimitBurst = 2
	}))
	room := srv.Factory.Classroom(t)
	stud := srv.ClientFor(t, room.Student)
	body := scanBody(room.Session.ID, "not-a-real-token")

	stud.Post(t, scanPath, body).RequireError(t, http.StatusBadRequest, "TOKEN_INVALID")
	stud.Post(t, scanPath, body).RequireError(t, http.StatusBadRequest, "TOKEN_INVALID")
	stud.Post(t, scanPath, body).RequireError(t, http.StatusTooManyRequests, "RATE_LIMITED")

	// Limits are per student.
	srv.AsStudent(t).Post(t, scanPath, body).RequireError(t, http.StatusBadRequest, "TOKEN_INVALID")

	srv.Clock.Advance(time.Second)
	stud.Post(t, scanPath, body).RequireError(t, http.StatusBadRequest, "TOKEN_INVALID")
}

func TestStudent_ListSubjects(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	room := srv.Factory.Classroom(t)
	srv.Factory.Subject(t, room.Professor.ID) // not enrolled

	var subjects []map[string]any
	srv.ClientFor(t, room.Student).Get(t, "/api/v1/student/subjects").RequireStatus(t, http.StatusOK).Data(t, &subjects)
	require.Len(t, subjects, 1)
	assertShape(t, subjectShape, subjects[0])
	assert.Equal(t, room.Subject.ID.String(), subjects[0]["id"])
}
