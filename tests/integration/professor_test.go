//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"fictin/internal/models"
	"fictin/internal/testutil"
)

func TestProfessor_CreateSubject(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	prof := srv.AsProfessor(t)

	subj := prof.Post(t, "/api/v1/subjects", map[string]string{"name": " Databases 101 ", "code": "cs101"}).
		RequireStatus(t, http.StatusCreated).DataMap(t)
	assertShape(t, subjectShape, subj)
	assert.Equal(t, "CS101", subj["code"], "code is upper-cased")
	assert.Equal(t, "Databases 101", subj["name"], "name is trimmed")
	assert.Equal(t, prof.User.ID.String(), subj["professor_id"], "owned by the caller")

	tests := []struct {
		name       string
		client     *testutil.Client
		body       any
		wantStatus int
		wantCode   string
	}{
		{"duplicate code", prof, map[string]string{"name": "Other", "code": "CS101"}, 409, "CONFLICT"},
		{"duplicate code differing in case", prof, map[string]string{"name": "Other", "code": "cs101"}, 409, "CONFLICT"},
		{"duplicate code from another professor", srv.AsProfessor(t), map[string]string{"name": "Other", "code": "CS101"}, 409, "CONFLICT"},
		{"missing name", prof, map[string]string{"code": "CS102"}, 422, "VALIDATION_ERROR"},
		{"code too short", prof, map[string]string{"name": "Databases", "code": "C"}, 422, "VALIDATION_ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.client.Post(t, "/api/v1/subjects", tt.body).RequireError(t, tt.wantStatus, tt.wantCode)
		})
	}
}

func TestProfessor_ListOnlyOwnSubjects(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	prof := srv.AsProfessor(t)
	mine := srv.Factory.Subject(t, prof.User.ID)
	srv.Factory.Subject(t, srv.Factory.User(t, models.RoleProfessor).ID)

	var subjects []map[string]any
	prof.Get(t, "/api/v1/subjects").RequireStatus(t, http.StatusOK).Data(t, &subjects)
	require.Len(t, subjects, 1)
	assert.Equal(t, mine.ID.String(), subjects[0]["id"])
}

// Empty lists are [] (never null), at the top level and nested, as
// FrontendDirections.md documents: the frontend calls .map on them.
func TestEmptyListsAreArrays(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	admin := srv.AsAdmin(t)
	prof := srv.AsProfessor(t)
	stud := srv.AsStudent(t)
	subj := srv.Factory.Subject(t, prof.User.ID)
	sess := srv.Factory.LiveSession(t, subj.ID)
	subjPath := "/api/v1/subjects/" + subj.ID.String()

	tests := []struct {
		name   string
		client *testutil.Client
		path   string
	}{
		{"own subjects (other professor)", srv.AsProfessor(t), "/api/v1/subjects"},
		{"schedules", prof, subjPath + "/schedules"},
		{"roster", prof, subjPath + "/enrollments"},
		{"session attendance", prof, "/api/v1/sessions/" + sess.ID.String() + "/attendance"},
		{"student subjects", stud, "/api/v1/student/subjects"},
		{"student history", stud, "/api/v1/student/attendance"},
		{"admin attendance", admin, "/api/v1/admin/attendance"},
		{"admin users, no match", admin, "/api/v1/admin/users?offset=1000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := tt.client.Get(t, tt.path).RequireStatus(t, http.StatusOK)
			assert.JSONEq(t, `{"data":[]}`, string(res.Body))
		})
	}

	t.Run("nested lists in subject detail", func(t *testing.T) {
		var detail map[string]json.RawMessage
		prof.Get(t, subjPath).RequireStatus(t, http.StatusOK).Data(t, &detail)
		assert.JSONEq(t, `[]`, string(detail["schedules"]))
		assert.JSONEq(t, `[]`, string(detail["roster"]))
	})
}

func TestProfessor_SubjectDetailUpdateDelete(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	prof := srv.AsProfessor(t)
	subj := srv.Factory.Subject(t, prof.User.ID)
	stud := srv.Factory.User(t, models.RoleStudent)
	srv.Factory.Enroll(t, subj.ID, stud.ID)
	path := "/api/v1/subjects/" + subj.ID.String()

	prof.Post(t, path+"/schedules", map[string]any{"day_of_week": 1, "start_time": "10:00", "end_time": "11:30", "room": "B204"}).
		RequireStatus(t, http.StatusCreated)

	var detail struct {
		Subject   map[string]any   `json:"subject"`
		Schedules []map[string]any `json:"schedules"`
		Roster    []map[string]any `json:"roster"`
	}
	prof.Get(t, path).RequireStatus(t, http.StatusOK).Data(t, &detail)
	assertShape(t, subjectShape, detail.Subject)
	require.Len(t, detail.Schedules, 1)
	assertShape(t, scheduleShape, detail.Schedules[0])
	require.Len(t, detail.Roster, 1)
	assertShape(t, rosterShape, detail.Roster[0])
	assert.Equal(t, stud.ID.String(), detail.Roster[0]["student_id"])

	updated := prof.Patch(t, path, map[string]string{"name": "Renamed", "code": "new1"}).RequireStatus(t, http.StatusOK).DataMap(t)
	assert.Equal(t, "Renamed", updated["name"])
	assert.Equal(t, "NEW1", updated["code"])

	// A session with attendance is removed together with the subject.
	sess := srv.Factory.LiveSession(t, subj.ID)
	srv.Factory.Attendance(t, sess.ID, stud.ID)

	res := prof.Delete(t, path).RequireStatus(t, http.StatusNoContent)
	assert.Empty(t, res.Body)
	prof.Get(t, path).RequireError(t, http.StatusNotFound, "NOT_FOUND")
	prof.Get(t, "/api/v1/sessions/"+sess.ID.String()).RequireError(t, http.StatusNotFound, "NOT_FOUND")
	prof.Delete(t, path).RequireError(t, http.StatusNotFound, "NOT_FOUND")
}

func TestProfessor_Schedules(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	prof := srv.AsProfessor(t)
	path := "/api/v1/subjects/" + srv.Factory.Subject(t, prof.User.ID).ID.String() + "/schedules"

	s := prof.Post(t, path, map[string]any{"day_of_week": 0, "start_time": "10:00", "end_time": "11:30"}).
		RequireStatus(t, http.StatusCreated).DataMap(t)
	assertShape(t, scheduleShape, s)
	assert.Equal(t, "10:00:00", s["start_time"])
	assert.Equal(t, "11:30:00", s["end_time"])

	tests := []struct {
		name string
		body map[string]any
	}{
		{"end before start", map[string]any{"day_of_week": 1, "start_time": "11:00", "end_time": "10:00"}},
		{"end equals start", map[string]any{"day_of_week": 1, "start_time": "10:00", "end_time": "10:00"}},
		{"bad time format", map[string]any{"day_of_week": 1, "start_time": "10am", "end_time": "11:00"}},
		{"day out of range", map[string]any{"day_of_week": 7, "start_time": "10:00", "end_time": "11:00"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prof.Post(t, path, tt.body).RequireError(t, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
		})
	}

	var list []map[string]any
	prof.Get(t, path).RequireStatus(t, http.StatusOK).Data(t, &list)
	assert.Len(t, list, 1)
}

// Times are compared as times of day, not as text, and stored normalized.
func TestProfessor_ScheduleTimesCompareAsTimes(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	prof := srv.AsProfessor(t)
	path := "/api/v1/subjects/" + srv.Factory.Subject(t, prof.User.ID).ID.String() + "/schedules"

	valid := []struct {
		start, end         string
		wantStart, wantEnd string
	}{
		{"9:30", "10:00", "09:30:00", "10:00:00"},
		{"08:05", "9:45", "08:05:00", "09:45:00"},
		{"10:00:30", "10:00:45", "10:00:30", "10:00:45"},
		{"00:00", "23:59:59", "00:00:00", "23:59:59"},
	}
	for _, tt := range valid {
		t.Run(tt.start+"-"+tt.end, func(t *testing.T) {
			s := prof.Post(t, path, map[string]any{"day_of_week": 1, "start_time": tt.start, "end_time": tt.end}).
				RequireStatus(t, http.StatusCreated).DataMap(t)
			assert.Equal(t, tt.wantStart, s["start_time"])
			assert.Equal(t, tt.wantEnd, s["end_time"])
		})
	}

	invalid := []struct{ name, start, end string }{
		{"same time, different precision", "10:00", "10:00:00"},
		{"end one second early", "10:00:01", "10:00"},
		{"single-digit end before start", "10:00", "9:59"},
		{"24:00 is not a time of day", "23:00", "24:00"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			prof.Post(t, path, map[string]any{"day_of_week": 1, "start_time": tt.start, "end_time": tt.end}).
				RequireError(t, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
		})
	}
}

func TestProfessor_Enrollments(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	prof := srv.AsProfessor(t)
	subj := srv.Factory.Subject(t, prof.User.ID)
	path := "/api/v1/subjects/" + subj.ID.String() + "/enrollments"
	byID := srv.Factory.User(t, models.RoleStudent)
	byCode := srv.Factory.User(t, models.RoleStudent)
	inactive := srv.Factory.User(t, models.RoleStudent, testutil.Inactive())
	otherProf := srv.Factory.User(t, models.RoleProfessor)

	e := prof.Post(t, path, map[string]string{"student_id": byID.ID.String()}).RequireStatus(t, http.StatusCreated).DataMap(t)
	assertShape(t, enrollmentShape, e)
	prof.Post(t, path, map[string]string{"student_code": *byCode.StudentCode}).RequireStatus(t, http.StatusCreated)

	tests := []struct {
		name       string
		body       map[string]string
		wantStatus int
		wantCode   string
	}{
		{"already enrolled", map[string]string{"student_id": byID.ID.String()}, 409, "CONFLICT"},
		{"unknown student", map[string]string{"student_code": "NOPE-404"}, 404, "NOT_FOUND"},
		{"not a student", map[string]string{"student_id": otherProf.ID.String()}, 422, "VALIDATION_ERROR"},
		{"inactive student", map[string]string{"student_id": inactive.ID.String()}, 422, "VALIDATION_ERROR"},
		{"neither id nor code", map[string]string{}, 422, "VALIDATION_ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prof.Post(t, path, tt.body).RequireError(t, tt.wantStatus, tt.wantCode)
		})
	}

	var roster []map[string]any
	prof.Get(t, path).RequireStatus(t, http.StatusOK).Data(t, &roster)
	assertListShape(t, rosterShape, roster)
	assert.Len(t, roster, 2)

	// The enrolled student now sees the subject.
	var subjects []map[string]any
	srv.ClientFor(t, byID).Get(t, "/api/v1/student/subjects").RequireStatus(t, http.StatusOK).Data(t, &subjects)
	require.Len(t, subjects, 1)
	assert.Equal(t, subj.ID.String(), subjects[0]["id"])
}

func TestProfessor_StartStopSession(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	prof := srv.AsProfessor(t)
	subj := srv.Factory.Subject(t, prof.User.ID)
	start := "/api/v1/sessions/" + subj.ID.String() + "/start"

	sess := prof.Post(t, start, nil).RequireStatus(t, http.StatusCreated).DataMap(t)
	assertShape(t, sessionShape, sess)
	assert.Equal(t, "live", sess["status"])
	assert.Contains(t, sess, "started_at")
	sessPath := "/api/v1/sessions/" + sess["id"].(string)

	view := prof.Get(t, sessPath).RequireStatus(t, http.StatusOK).DataMap(t)
	assertShape(t, sessionViewShape, view)
	assert.Equal(t, true, view["qr_rotating"])

	t.Run("only one live session per subject", func(t *testing.T) {
		prof.Post(t, start, map[string]any{}).RequireError(t, http.StatusConflict, "CONFLICT")
	})

	t.Run("polling fallback returns the current token", func(t *testing.T) {
		var msg map[string]any
		require.Eventually(t, func() bool {
			res := prof.Get(t, sessPath+"/qr/current")
			if res.Status != http.StatusOK {
				return false
			}
			msg = res.DataMap(t)
			return true
		}, 5*time.Second, 10*time.Millisecond)
		assertShape(t, tokenMessageShape, msg)
		assert.Equal(t, sess["id"], msg["session_id"])
	})

	stopped := prof.Post(t, sessPath+"/stop", nil).RequireStatus(t, http.StatusOK).DataMap(t)
	assertShape(t, sessionShape, stopped)
	assert.Equal(t, "ended", stopped["status"])
	assert.Contains(t, stopped, "ended_at")

	prof.Post(t, sessPath+"/stop", nil).RequireError(t, http.StatusConflict, "CONFLICT")
	prof.Get(t, sessPath+"/qr/current").RequireError(t, http.StatusConflict, "SESSION_NOT_LIVE")
	view = prof.Get(t, sessPath).RequireStatus(t, http.StatusOK).DataMap(t)
	assert.Equal(t, false, view["qr_rotating"])

	// A new session can start once the previous one ended.
	prof.Post(t, start, nil).RequireStatus(t, http.StatusCreated)
}

func TestProfessor_StartSessionWithSchedule(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	prof := srv.AsProfessor(t)
	subj := srv.Factory.Subject(t, prof.User.ID)
	other := srv.Factory.Subject(t, prof.User.ID)
	sched := prof.Post(t, "/api/v1/subjects/"+other.ID.String()+"/schedules",
		map[string]any{"day_of_week": 1, "start_time": "10:00", "end_time": "11:00"}).
		RequireStatus(t, http.StatusCreated).DataMap(t)

	prof.Post(t, "/api/v1/sessions/"+subj.ID.String()+"/start", map[string]string{"schedule_id": sched["id"].(string)}).
		RequireError(t, http.StatusUnprocessableEntity, "VALIDATION_ERROR")

	sess := prof.Post(t, "/api/v1/sessions/"+other.ID.String()+"/start", map[string]string{"schedule_id": sched["id"].(string)}).
		RequireStatus(t, http.StatusCreated).DataMap(t)
	assert.Equal(t, sched["id"], sess["schedule_id"])

	prof.Post(t, "/api/v1/sessions/00000000-0000-0000-0000-000000000000/start", nil).
		RequireError(t, http.StatusNotFound, "NOT_FOUND")
}

func TestProfessor_UpdateSubjectValidation(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	prof := srv.AsProfessor(t)
	subj := srv.Factory.Subject(t, prof.User.ID, testutil.WithSubjectCode("KEEP1"))
	srv.Factory.Subject(t, srv.Factory.User(t, models.RoleProfessor).ID, testutil.WithSubjectCode("TAKEN1"))
	path := "/api/v1/subjects/" + subj.ID.String()

	tests := []struct {
		name       string
		body       any
		wantStatus int
		wantCode   string
	}{
		{"code taken by another subject", map[string]string{"code": "TAKEN1"}, 409, "CONFLICT"},
		{"code taken, different case", map[string]string{"code": "taken1"}, 409, "CONFLICT"},
		{"name too short", map[string]string{"name": "X"}, 422, "VALIDATION_ERROR"},
		{"unknown field", map[string]string{"professor_id": prof.User.ID.String()}, 400, "BAD_REQUEST"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prof.Patch(t, path, tt.body).RequireError(t, tt.wantStatus, tt.wantCode)
		})
	}

	t.Run("empty patch keeps the subject unchanged", func(t *testing.T) {
		got := prof.Patch(t, path, map[string]string{}).RequireStatus(t, http.StatusOK).DataMap(t)
		assert.Equal(t, "KEEP1", got["code"])
	})
	t.Run("keeping its own code is not a conflict", func(t *testing.T) {
		prof.Patch(t, path, map[string]string{"code": "keep1"}).RequireStatus(t, http.StatusOK)
	})
}

func TestProfessor_EnrollByCodeTrimsWhitespace(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	prof := srv.AsProfessor(t)
	subj := srv.Factory.Subject(t, prof.User.ID)
	stud := srv.Factory.User(t, models.RoleStudent, testutil.WithStudentCode("S-777"))

	var e map[string]any
	prof.Post(t, "/api/v1/subjects/"+subj.ID.String()+"/enrollments", map[string]string{"student_code": "  S-777 "}).
		RequireStatus(t, http.StatusCreated).Data(t, &e)
	assert.Equal(t, stud.ID.String(), e["student_id"])

	prof.Post(t, "/api/v1/subjects/"+subj.ID.String()+"/enrollments", map[string]string{"student_code": "   "}).
		RequireError(t, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
}
