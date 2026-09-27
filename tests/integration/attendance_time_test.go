//go:build integration

package integration

import (
	"encoding/csv"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"fictin/internal/testutil"
)

// scanned_at must be RFC3339 in UTC ("2026-09-13T10:15:30Z") everywhere it
// appears, as FrontendDirections.md §2 promises, regardless of the server's
// or database's time zone.
func TestAttendance_ScannedAtIsRFC3339UTCEverywhere(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	room := srv.Factory.Classroom(t)
	tok := srv.Factory.QRToken(t, room.Session.ID, srv.Clock.Now(), time.Minute)

	isUTC := func(t *testing.T, where, v string) time.Time {
		t.Helper()
		ts, err := time.Parse(time.RFC3339, v)
		require.NoError(t, err, "%s: %q is not RFC3339", where, v)
		assert.True(t, strings.HasSuffix(v, "Z"), "%s: %q is not in UTC", where, v)
		return ts
	}

	var scan struct {
		ScannedAt string `json:"scanned_at"`
		Record    struct {
			ScannedAt string `json:"scanned_at"`
		} `json:"record"`
	}
	srv.ClientFor(t, room.Student).Post(t, "/api/v1/attendance/scan",
		map[string]string{"session_id": room.Session.ID.String(), "token": tok.Token}).
		RequireStatus(t, http.StatusOK).Data(t, &scan)
	want := isUTC(t, "scan response", scan.ScannedAt)
	isUTC(t, "scan response record", scan.Record.ScannedAt)

	lists := map[string]*testutil.Response{
		"student history":    srv.ClientFor(t, room.Student).Get(t, "/api/v1/student/attendance"),
		"session attendance": srv.ClientFor(t, room.Professor).Get(t, "/api/v1/sessions/"+room.Session.ID.String()+"/attendance"),
		"admin attendance":   srv.AsAdmin(t).Get(t, "/api/v1/admin/attendance"),
	}
	for where, res := range lists {
		t.Run(where, func(t *testing.T) {
			var rows []struct {
				ScannedAt string `json:"scanned_at"`
			}
			res.RequireStatus(t, http.StatusOK).Data(t, &rows)
			require.Len(t, rows, 1)
			assert.Equal(t, want, isUTC(t, where, rows[0].ScannedAt))
		})
	}

	t.Run("admin csv export", func(t *testing.T) {
		res := srv.AsAdmin(t).Get(t, "/api/v1/admin/attendance?format=csv").RequireStatus(t, http.StatusOK)
		records, err := csv.NewReader(strings.NewReader(string(res.Body))).ReadAll()
		require.NoError(t, err)
		require.Len(t, records, 2)
		assert.Equal(t, want, isUTC(t, "csv", records[1][6]))
	})
}
