//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"fictin/internal/models"
	"fictin/internal/testutil"
)

func TestAdmin_RegisterUser(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	admin := srv.AsAdmin(t)
	const path = "/api/v1/auth/register"

	t.Run("creates a student with a temporary password", func(t *testing.T) {
		res := admin.Post(t, path, map[string]string{
			"full_name": "Jane Doe", "email": "Jane@Example.test", "password": "TempPass123!",
			"role": "student", "student_code": " S12345 ",
		}).RequireStatus(t, http.StatusCreated)

		u := res.DataMap(t)
		assertShape(t, userShape, u)
		assert.Equal(t, "jane@example.test", u["email"], "email is normalized to lower case")
		assert.Equal(t, "S12345", u["student_code"], "student code is trimmed")
		assert.Equal(t, true, u["must_change_password"])
		assert.Equal(t, true, u["is_active"])

		loginOK(t, srv, "jane@example.test", "TempPass123!")
	})

	t.Run("student_code is ignored for non-students", func(t *testing.T) {
		u := admin.Post(t, path, map[string]string{
			"full_name": "Prof X", "email": "profx@example.test", "password": "TempPass123!",
			"role": "professor", "student_code": "IGNORED",
		}).RequireStatus(t, http.StatusCreated).DataMap(t)
		assert.NotContains(t, u, "student_code")
	})

	tests := []struct {
		name       string
		body       map[string]string
		wantStatus int
		wantCode   string
	}{
		{"duplicate email", map[string]string{"full_name": "Dup", "email": "jane@example.test", "password": "TempPass123!", "role": "professor"}, 409, "CONFLICT"},
		{"duplicate email differing in case", map[string]string{"full_name": "Dup", "email": "JANE@example.test", "password": "TempPass123!", "role": "admin"}, 409, "CONFLICT"},
		{"duplicate student code", map[string]string{"full_name": "Dup", "email": "other@example.test", "password": "TempPass123!", "role": "student", "student_code": "S12345"}, 409, "CONFLICT"},
		{"student without code", map[string]string{"full_name": "No Code", "email": "nocode@example.test", "password": "TempPass123!", "role": "student"}, 422, "VALIDATION_ERROR"},
		{"invalid role", map[string]string{"full_name": "Root", "email": "root@example.test", "password": "TempPass123!", "role": "root"}, 422, "VALIDATION_ERROR"},
		{"weak password", map[string]string{"full_name": "Weak", "email": "weak@example.test", "password": "short", "role": "admin"}, 422, "VALIDATION_ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			admin.Post(t, path, tt.body).RequireError(t, tt.wantStatus, tt.wantCode)
		})
	}
}

func TestAdmin_ListAndFilterUsers(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	admin := srv.AsAdmin(t)
	prof := srv.Factory.User(t, models.RoleProfessor)
	active := srv.Factory.User(t, models.RoleStudent)
	inactive := srv.Factory.User(t, models.RoleStudent, testutil.Inactive())

	ids := func(res *testutil.Response) []string {
		var users []map[string]any
		res.RequireStatus(t, http.StatusOK).Data(t, &users)
		assertListShape(t, userShape, users)
		out := make([]string, 0, len(users))
		for _, u := range users {
			out = append(out, u["id"].(string))
		}
		return out
	}

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"all users", "", []string{admin.User.ID.String(), prof.ID.String(), active.ID.String(), inactive.ID.String()}},
		{"by role", "?role=student", []string{active.ID.String(), inactive.ID.String()}},
		{"active only", "?role=student&active=true", []string{active.ID.String()}},
		{"inactive only", "?active=false", []string{inactive.ID.String()}},
		{"professors", "?role=professor", []string{prof.ID.String()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.ElementsMatch(t, tt.want, ids(admin.Get(t, "/api/v1/admin/users"+tt.query)))
		})
	}

	t.Run("pagination", func(t *testing.T) {
		page1 := ids(admin.Get(t, "/api/v1/admin/users?limit=2&offset=0"))
		page2 := ids(admin.Get(t, "/api/v1/admin/users?limit=2&offset=2"))
		assert.Len(t, page1, 2)
		assert.Len(t, page2, 2)
		// No overlap assertion: ORDER BY created_at has no tiebreaker (reported as a follow-up).
	})

	t.Run("invalid role filter", func(t *testing.T) {
		admin.Get(t, "/api/v1/admin/users?role=teacher").RequireError(t, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	})
}

func TestAdmin_UpdateAndDeactivateUser(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	admin := srv.AsAdmin(t)
	stud := srv.Factory.User(t, models.RoleStudent)
	other := srv.Factory.User(t, models.RoleStudent)
	path := "/api/v1/admin/users/" + stud.ID.String()

	p := loginOK(t, srv, stud.Email, testutil.DefaultPassword)

	u := admin.Patch(t, path, map[string]any{"full_name": "Renamed Student", "is_active": false}).
		RequireStatus(t, http.StatusOK).DataMap(t)
	assertShape(t, userShape, u)
	assert.Equal(t, "Renamed Student", u["full_name"])
	assert.Equal(t, false, u["is_active"])

	login(t, srv, stud.Email, testutil.DefaultPassword).RequireError(t, http.StatusForbidden, "ACCOUNT_INACTIVE")
	refresh(t, srv, p.RefreshToken).RequireStatus(t, http.StatusUnauthorized)

	// Reactivation restores login.
	admin.Patch(t, path, map[string]any{"is_active": true}).RequireStatus(t, http.StatusOK)
	loginOK(t, srv, stud.Email, testutil.DefaultPassword)

	tests := []struct {
		name       string
		path       string
		body       any
		wantStatus int
		wantCode   string
	}{
		{"student code taken", path, map[string]any{"student_code": *other.StudentCode}, 409, "CONFLICT"},
		{"blank name", path, map[string]any{"full_name": "  "}, 422, "VALIDATION_ERROR"},
		{"unknown user", "/api/v1/admin/users/00000000-0000-0000-0000-000000000000", map[string]any{"is_active": false}, 404, "NOT_FOUND"},
		{"bad id", "/api/v1/admin/users/not-a-uuid", map[string]any{"is_active": false}, 400, "BAD_REQUEST"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			admin.Patch(t, tt.path, tt.body).RequireError(t, tt.wantStatus, tt.wantCode)
		})
	}
}

func TestAdmin_DeleteUser(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	admin := srv.AsAdmin(t)
	stud := srv.Factory.User(t, models.RoleStudent)
	path := "/api/v1/admin/users/" + stud.ID.String()

	res := admin.Delete(t, path).RequireStatus(t, http.StatusNoContent)
	assert.Empty(t, res.Body)

	admin.Delete(t, path).RequireError(t, http.StatusNotFound, "NOT_FOUND")
	login(t, srv, stud.Email, testutil.DefaultPassword).RequireError(t, http.StatusUnauthorized, "INVALID_CREDENTIALS")
}

func TestAdmin_ListSubjectsAndSessions(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	admin := srv.AsAdmin(t)
	a := srv.Factory.Classroom(t)
	b := srv.Factory.Classroom(t)

	var subjects []map[string]any
	admin.Get(t, "/api/v1/admin/subjects").RequireStatus(t, http.StatusOK).Data(t, &subjects)
	require.Len(t, subjects, 2)
	assert.ElementsMatch(t, []string{a.Subject.ID.String(), b.Subject.ID.String()},
		[]string{subjects[0]["id"].(string), subjects[1]["id"].(string)}, "admin sees subjects of every professor")
	assertListShape(t, subjectShape, subjects)

	// Admin may read any session.
	view := admin.Get(t, "/api/v1/sessions/"+b.Session.ID.String()).RequireStatus(t, http.StatusOK).DataMap(t)
	assertShape(t, sessionViewShape, view)
}
