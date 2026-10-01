//go:build integration

package integration

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Field sets from FrontendDirections.md §4. Optional (omitempty) fields are
// listed separately: they may be absent but nothing outside the two lists may
// appear, so a renamed or leaked field (e.g. password_hash) fails the test.
type shape struct {
	required []string
	optional []string
	times    []string // RFC3339 timestamp fields
}

var (
	userShape = shape{
		required: []string{"id", "full_name", "email", "role", "is_active", "must_change_password", "created_at", "updated_at"},
		optional: []string{"student_code"},
		times:    []string{"created_at", "updated_at"},
	}
	subjectShape = shape{
		required: []string{"id", "name", "code", "professor_id", "created_at", "updated_at"},
		times:    []string{"created_at", "updated_at"},
	}
	sessionShape = shape{
		required: []string{"id", "subject_id", "status", "created_at"},
		optional: []string{"schedule_id", "started_at", "ended_at"},
		times:    []string{"created_at", "started_at", "ended_at"},
	}
	sessionViewShape = shape{
		required: append(slices.Clone(sessionShape.required), "qr_rotating"),
		optional: sessionShape.optional,
		times:    sessionShape.times,
	}
	scheduleShape = shape{
		required: []string{"id", "subject_id", "day_of_week", "start_time", "end_time", "created_at"},
		optional: []string{"room"},
		times:    []string{"created_at"},
	}
	enrollmentShape = shape{
		required: []string{"id", "subject_id", "student_id", "created_at"},
		times:    []string{"created_at"},
	}
	rosterShape = shape{
		required: []string{"enrollment_id", "student_id", "full_name", "email", "enrolled_at"},
		optional: []string{"student_code"},
		times:    []string{"enrolled_at"},
	}
	attendanceRowShape = shape{
		required: []string{"id", "session_id", "subject_id", "subject_name", "subject_code", "student_id", "student_name", "scanned_at"},
		optional: []string{"student_code"},
		times:    []string{"scanned_at"},
	}
	attendanceRecordShape = shape{
		required: []string{"id", "session_id", "student_id", "scanned_at"},
		optional: []string{"qr_token_id"},
		times:    []string{"scanned_at"},
	}
	tokenMessageShape = shape{
		required: []string{"session_id", "token", "issued_at", "expires_at"},
		times:    []string{"issued_at", "expires_at"},
	}
	tokenPairShape = shape{
		required: []string{"access_token", "refresh_token", "token_type", "expires_in", "user"},
	}
)

// assertShape checks the exact JSON field set and that timestamps are RFC3339
// in UTC ("...Z"), as FrontendDirections.md §2 promises.
func assertShape(t *testing.T, s shape, obj map[string]any) {
	t.Helper()
	for _, k := range s.required {
		assert.Contains(t, obj, k, "missing field %q", k)
	}
	for k := range obj {
		if !slices.Contains(s.required, k) && !slices.Contains(s.optional, k) {
			assert.Failf(t, "unexpected field", "field %q is not part of the documented contract", k)
		}
	}
	for _, k := range s.times {
		if v, ok := obj[k]; ok {
			str, isStr := v.(string)
			if assert.True(t, isStr, "%s must be a string", k) {
				_, err := time.Parse(time.RFC3339, str)
				assert.NoError(t, err, "%s = %q is not RFC3339", k, str)
				assert.True(t, strings.HasSuffix(str, "Z"), "%s = %q is not in UTC", k, str)
			}
		}
	}
}

// assertListShape checks every element of a JSON array.
func assertListShape(t *testing.T, s shape, list []map[string]any) {
	t.Helper()
	for _, obj := range list {
		assertShape(t, s, obj)
	}
}
