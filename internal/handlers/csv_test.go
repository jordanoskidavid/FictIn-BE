package handlers

import (
	"encoding/csv"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"fictin/internal/repository"
)

var csvHeader = []string{"record_id", "session_id", "subject_code", "subject_name", "student_code", "student_name", "scanned_at"}

func TestWriteAttendanceCSV(t *testing.T) {
	code := "S-001"
	recID, sessID := uuid.New(), uuid.New()

	tests := []struct {
		name     string
		rows     []repository.AttendanceRow
		wantRows [][]string
	}{
		{
			name:     "empty data writes only the header",
			rows:     nil,
			wantRows: [][]string{csvHeader},
		},
		{
			name: "plain row",
			rows: []repository.AttendanceRow{{
				ID: recID, SessionID: sessID, SubjectCode: "CS101", SubjectName: "Databases",
				StudentCode: &code, StudentName: "Ana Petrova", ScannedAt: "2026-09-13T10:15:30Z",
			}},
			wantRows: [][]string{csvHeader, {recID.String(), sessID.String(), "CS101", "Databases", "S-001", "Ana Petrova", "2026-09-13T10:15:30Z"}},
		},
		{
			name: "commas, quotes and newlines survive a round trip",
			rows: []repository.AttendanceRow{{
				ID: recID, SessionID: sessID, SubjectCode: "CS,102", SubjectName: `Intro to "Go"`,
				StudentName: "Line one\nLine two", ScannedAt: "2026-09-13T10:15:30Z",
			}},
			wantRows: [][]string{csvHeader, {recID.String(), sessID.String(), "CS,102", `Intro to "Go"`, "", "Line one\nLine two", "2026-09-13T10:15:30Z"}},
		},
		{
			name:     "missing student code is an empty cell",
			rows:     []repository.AttendanceRow{{ID: recID, SessionID: sessID, StudentCode: nil}},
			wantRows: [][]string{csvHeader, {recID.String(), sessID.String(), "", "", "", "", ""}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeAttendanceCSV(rec, tt.rows)

			assert.Equal(t, "text/csv; charset=utf-8", rec.Header().Get("Content-Type"))
			assert.Equal(t, `attachment; filename="attendance.csv"`, rec.Header().Get("Content-Disposition"))

			got, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
			require.NoError(t, err)
			assert.Equal(t, tt.wantRows, got)
		})
	}
}

func TestWriteAttendanceCSVQuotesSpecialCharacters(t *testing.T) {
	rec := httptest.NewRecorder()
	writeAttendanceCSV(rec, []repository.AttendanceRow{{SubjectName: `a,"b"`}})
	assert.Contains(t, rec.Body.String(), `"a,""b"""`, "RFC 4180 quoting")
}
