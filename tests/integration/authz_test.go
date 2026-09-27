//go:build integration

package integration

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"fictin/internal/models"
	"fictin/internal/testutil"
)

// protectedRoutes mirrors internal/router. Paths use random IDs: role checks
// happen before any lookup, so no data is needed. When you add a route,
// add it here too.
func protectedRoutes() []struct {
	method, path string
	allowed      []models.Role
} {
	id := uuid.NewString()
	admin := []models.Role{models.RoleAdmin}
	prof := []models.Role{models.RoleProfessor}
	student := []models.Role{models.RoleStudent}
	anyone := []models.Role{models.RoleAdmin, models.RoleProfessor, models.RoleStudent}

	return []struct {
		method, path string
		allowed      []models.Role
	}{
		{"GET", "/api/v1/auth/me", anyone},
		{"POST", "/api/v1/auth/change-password", anyone},
		{"POST", "/api/v1/auth/register", admin},

		{"GET", "/api/v1/admin/users", admin},
		{"PATCH", "/api/v1/admin/users/" + id, admin},
		{"DELETE", "/api/v1/admin/users/" + id, admin},
		{"GET", "/api/v1/admin/subjects", admin},
		{"GET", "/api/v1/admin/attendance", admin},

		{"POST", "/api/v1/subjects", prof},
		{"GET", "/api/v1/subjects", prof},
		{"GET", "/api/v1/subjects/" + id, prof},
		{"PATCH", "/api/v1/subjects/" + id, prof},
		{"DELETE", "/api/v1/subjects/" + id, prof},
		{"POST", "/api/v1/subjects/" + id + "/schedules", prof},
		{"GET", "/api/v1/subjects/" + id + "/schedules", prof},
		{"POST", "/api/v1/subjects/" + id + "/enrollments", prof},
		{"GET", "/api/v1/subjects/" + id + "/enrollments", prof},

		{"POST", "/api/v1/sessions/" + id + "/start", prof},
		{"POST", "/api/v1/sessions/" + id + "/stop", prof},
		{"GET", "/api/v1/sessions/" + id + "/qr/current", prof},
		{"GET", "/api/v1/sessions/" + id + "/attendance", prof},
		{"GET", "/api/v1/sessions/" + id, []models.Role{models.RoleProfessor, models.RoleAdmin}},

		{"GET", "/api/v1/student/subjects", student},
		{"GET", "/api/v1/student/attendance", student},
		{"POST", "/api/v1/attendance/scan", student},

		{"GET", "/ws/sessions/" + id + "/qr", prof},
	}
}

func TestAuthz_GuestsGet401OnEveryProtectedRoute(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)

	for _, r := range protectedRoutes() {
		t.Run(r.method+" "+routeName(r.path), func(t *testing.T) {
			srv.Guest().Do(t, r.method, r.path, nil).RequireError(t, http.StatusUnauthorized, "UNAUTHORIZED")
		})
	}
}

func TestAuthz_InvalidTokenGets401(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)

	for _, r := range protectedRoutes() {
		t.Run(r.method+" "+routeName(r.path), func(t *testing.T) {
			srv.WithToken("not.a.jwt").Do(t, r.method, r.path, nil).RequireError(t, http.StatusUnauthorized, "UNAUTHORIZED")
		})
	}
}

func TestAuthz_WrongRoleGets403OnEveryProtectedRoute(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	clients := map[models.Role]*testutil.Client{
		models.RoleAdmin:     srv.AsAdmin(t),
		models.RoleProfessor: srv.AsProfessor(t),
		models.RoleStudent:   srv.AsStudent(t),
	}

	for _, r := range protectedRoutes() {
		for role, c := range clients {
			if slices.Contains(r.allowed, role) {
				continue
			}
			t.Run(string(role)+" "+r.method+" "+routeName(r.path), func(t *testing.T) {
				c.Do(t, r.method, r.path, nil).RequireError(t, http.StatusForbidden, "FORBIDDEN")
			})
		}
	}
}

func TestAuthz_PublicRoutesNeedNoToken(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)

	// These reach the handler (validation error), proving no auth middleware.
	for _, path := range []string{"/api/v1/auth/login", "/api/v1/auth/refresh", "/api/v1/auth/logout"} {
		t.Run(path, func(t *testing.T) {
			srv.Guest().Post(t, path, map[string]string{}).RequireError(t, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
		})
	}
}

// Ownership: a professor cannot touch another professor's subject or session.
func TestAuthz_ProfessorsCannotAccessOthersResources(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	room := srv.Factory.Classroom(t)
	intruder := srv.AsProfessor(t)
	subj, sess := room.Subject.ID.String(), room.Session.ID.String()

	tests := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/subjects/" + subj, nil},
		{"PATCH", "/api/v1/subjects/" + subj, map[string]string{"name": "Hijacked"}},
		{"DELETE", "/api/v1/subjects/" + subj, nil},
		{"POST", "/api/v1/subjects/" + subj + "/schedules", map[string]any{"day_of_week": 1, "start_time": "10:00", "end_time": "11:00"}},
		{"GET", "/api/v1/subjects/" + subj + "/schedules", nil},
		{"POST", "/api/v1/subjects/" + subj + "/enrollments", map[string]string{"student_id": room.Student.ID.String()}},
		{"GET", "/api/v1/subjects/" + subj + "/enrollments", nil},
		{"POST", "/api/v1/sessions/" + subj + "/start", nil},
		{"POST", "/api/v1/sessions/" + sess + "/stop", nil},
		{"GET", "/api/v1/sessions/" + sess, nil},
		{"GET", "/api/v1/sessions/" + sess + "/qr/current", nil},
		{"GET", "/api/v1/sessions/" + sess + "/attendance", nil},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+routeName(tt.path), func(t *testing.T) {
			intruder.Do(t, tt.method, tt.path, tt.body).RequireError(t, http.StatusForbidden, "FORBIDDEN")
		})
	}
}

// routeName replaces UUIDs so subtest names stay stable and readable.
func routeName(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if _, err := uuid.Parse(p); err == nil {
			parts[i] = "{id}"
		}
	}
	return strings.Join(parts, "/")
}
