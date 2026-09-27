//go:build integration

package integration

import (
	"encoding/csv"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"fictin/internal/models"
	"fictin/internal/testutil"
)

var exportHeader = []string{"record_id", "session_id", "subject_code", "subject_name", "student_code", "student_name", "scanned_at"}

func readCSV(t *testing.T, res *testutil.Response) [][]string {
	t.Helper()
	res.RequireStatus(t, http.StatusOK)
	assert.Equal(t, "text/csv; charset=utf-8", res.Header.Get("Content-Type"))
	assert.Equal(t, `attachment; filename="attendance.csv"`, res.Header.Get("Content-Disposition"))
	records, err := csv.NewReader(strings.NewReader(string(res.Body))).ReadAll()
	require.NoError(t, err)
	require.NotEmpty(t, records)
	assert.Equal(t, exportHeader, records[0])
	return records[1:]
}

func TestExport_AdminAttendanceCSVAndFilters(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	admin := srv.AsAdmin(t)
	f := srv.Factory

	prof := f.User(t, models.RoleProfessor)
	db := f.Subject(t, prof.ID, testutil.WithSubjectCode("DB101"), testutil.WithSubjectName(`Databases, "Advanced"`))
	ai := f.Subject(t, prof.ID, testutil.WithSubjectCode("AI201"))
	ana := f.User(t, models.RoleStudent, testutil.WithFullName("Ana, Petrova"), testutil.WithStudentCode("S-1"))
	ben := f.User(t, models.RoleStudent, testutil.WithFullName("Ben Stone"), testutil.WithStudentCode("S-2"))
	for _, s := range []*models.Subject{db, ai} {
		f.Enroll(t, s.ID, ana.ID)
		f.Enroll(t, s.ID, ben.ID)
	}
	dbSess := f.LiveSession(t, db.ID)
	aiSess := f.LiveSession(t, ai.ID)
	recAnaDB := f.Attendance(t, dbSess.ID, ana.ID)
	f.Attendance(t, dbSess.ID, ben.ID)
	f.Attendance(t, aiSess.ID, ana.ID)

	t.Run("csv content with escaping", func(t *testing.T) {
		rows := readCSV(t, admin.Get(t, "/api/v1/admin/attendance?format=csv&session_id="+dbSess.ID.String()+"&student_id="+ana.ID.String()))
		require.Len(t, rows, 1)
		assert.Equal(t, []string{
			recAnaDB.ID.String(), dbSess.ID.String(), "DB101", `Databases, "Advanced"`, "S-1", "Ana, Petrova",
			recAnaDB.ScannedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}, rows[0])
	})

	filters := []struct {
		name  string
		query string
		want  int
	}{
		{"no filter", "", 3},
		{"by session", "session_id=" + dbSess.ID.String(), 2},
		{"by subject", "subject_id=" + ai.ID.String(), 1},
		{"by student", "student_id=" + ana.ID.String(), 2},
		{"combined", "subject_id=" + db.ID.String() + "&student_id=" + ben.ID.String(), 1},
		{"limit", "limit=2", 2},
		{"offset past end", "offset=10", 0},
	}
	for _, tt := range filters {
		t.Run("csv "+tt.name, func(t *testing.T) {
			assert.Len(t, readCSV(t, admin.Get(t, "/api/v1/admin/attendance?format=csv&"+tt.query)), tt.want)
		})
		t.Run("json "+tt.name, func(t *testing.T) {
			var rows []map[string]any
			admin.Get(t, "/api/v1/admin/attendance?"+tt.query).RequireStatus(t, http.StatusOK).Data(t, &rows)
			assert.Len(t, rows, tt.want)
			assertListShape(t, attendanceRowShape, rows)
		})
	}

	t.Run("empty result is a header-only csv", func(t *testing.T) {
		empty := f.LiveSession(t, f.Subject(t, prof.ID).ID)
		assert.Empty(t, readCSV(t, admin.Get(t, "/api/v1/admin/attendance?format=csv&session_id="+empty.ID.String())))
	})

	t.Run("invalid filter", func(t *testing.T) {
		for _, q := range []string{"session_id=x", "subject_id=x", "student_id=x"} {
			admin.Get(t, "/api/v1/admin/attendance?"+q).RequireError(t, http.StatusBadRequest, "BAD_REQUEST")
		}
	})
}

// A professor sees only their own session's attendance (JSON); there is no
// professor CSV export yet.
func TestExport_ProfessorSessionAttendance(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	room := srv.Factory.Classroom(t)
	srv.Factory.Attendance(t, room.Session.ID, room.Student.ID)
	other := srv.Factory.Classroom(t)
	srv.Factory.Attendance(t, other.Session.ID, other.Student.ID)

	var rows []map[string]any
	srv.ClientFor(t, room.Professor).Get(t, "/api/v1/sessions/"+room.Session.ID.String()+"/attendance").
		RequireStatus(t, http.StatusOK).Data(t, &rows)
	require.Len(t, rows, 1)
	assert.Equal(t, room.Student.ID.String(), rows[0]["student_id"])
}
