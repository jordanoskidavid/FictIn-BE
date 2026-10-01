//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"fictin/internal/auth"
	"fictin/internal/models"
	"fictin/internal/testutil"
)

type tokenPair struct {
	AccessToken  string         `json:"access_token"`
	RefreshToken string         `json:"refresh_token"`
	TokenType    string         `json:"token_type"`
	ExpiresIn    int            `json:"expires_in"`
	User         map[string]any `json:"user"`
}

func login(t *testing.T, srv *testutil.Server, email, password string) *testutil.Response {
	t.Helper()
	return srv.Guest().Post(t, "/api/v1/auth/login", map[string]string{"email": email, "password": password})
}

func loginOK(t *testing.T, srv *testutil.Server, email, password string) tokenPair {
	t.Helper()
	var p tokenPair
	login(t, srv, email, password).RequireStatus(t, http.StatusOK).Data(t, &p)
	return p
}

func refresh(t *testing.T, srv *testutil.Server, token string) *testutil.Response {
	t.Helper()
	return srv.Guest().Post(t, "/api/v1/auth/refresh", map[string]string{"refresh_token": token})
}

func TestAuth_LoginSuccess(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	u := srv.Factory.User(t, models.RoleProfessor, testutil.WithEmail("prof@example.test"))

	// Email matching is case-insensitive.
	res := login(t, srv, "PROF@Example.test", testutil.DefaultPassword).RequireStatus(t, http.StatusOK)

	raw := res.DataMap(t)
	assertShape(t, tokenPairShape, raw)
	assertShape(t, userShape, raw["user"].(map[string]any))

	var p tokenPair
	res.Data(t, &p)
	assert.Equal(t, "Bearer", p.TokenType)
	assert.Equal(t, 30*60, p.ExpiresIn)
	assert.Equal(t, u.ID.String(), p.User["id"])
	assert.Equal(t, "professor", p.User["role"])
	assert.NotEmpty(t, p.RefreshToken)

	// The issued access token works.
	srv.WithToken(p.AccessToken).Get(t, "/api/v1/auth/me").RequireStatus(t, http.StatusOK)
}

func TestAuth_LoginFailures(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	srv.Factory.User(t, models.RoleStudent, testutil.WithEmail("active@example.test"))
	srv.Factory.User(t, models.RoleStudent, testutil.WithEmail("inactive@example.test"), testutil.Inactive())

	tests := []struct {
		name       string
		body       any
		wantStatus int
		wantCode   string
	}{
		{"wrong password", map[string]string{"email": "active@example.test", "password": "wrong-password"}, 401, "INVALID_CREDENTIALS"},
		{"unknown email", map[string]string{"email": "nobody@example.test", "password": testutil.DefaultPassword}, 401, "INVALID_CREDENTIALS"},
		{"deactivated account", map[string]string{"email": "inactive@example.test", "password": testutil.DefaultPassword}, 403, "ACCOUNT_INACTIVE"},
		{"deactivated account with wrong password does not reveal status", map[string]string{"email": "inactive@example.test", "password": "wrong"}, 401, "INVALID_CREDENTIALS"},
		{"invalid email format", map[string]string{"email": "nope", "password": "x"}, 422, "VALIDATION_ERROR"},
		{"missing password", map[string]string{"email": "active@example.test"}, 422, "VALIDATION_ERROR"},
		{"unknown field", map[string]any{"email": "active@example.test", "password": "x", "role": "admin"}, 400, "BAD_REQUEST"},
		{"empty body", "", 400, "BAD_REQUEST"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv.Guest().Post(t, "/api/v1/auth/login", tt.body).RequireError(t, tt.wantStatus, tt.wantCode)
		})
	}
}

func TestAuth_MustChangePasswordFlow(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	u := srv.Factory.User(t, models.RoleStudent, testutil.WithPassword("TempPass123!"), testutil.MustChangePassword())

	p := loginOK(t, srv, u.Email, "TempPass123!")
	assert.Equal(t, true, p.User["must_change_password"], "frontend uses this flag to force the change-password screen")

	c := srv.WithToken(p.AccessToken)
	const path = "/api/v1/auth/change-password"

	t.Run("wrong current password", func(t *testing.T) {
		c.Post(t, path, map[string]string{"current_password": "nope", "new_password": "NewPass123!"}).
			RequireError(t, http.StatusUnauthorized, "INVALID_CREDENTIALS")
	})
	t.Run("new password equals current", func(t *testing.T) {
		c.Post(t, path, map[string]string{"current_password": "TempPass123!", "new_password": "TempPass123!"}).
			RequireError(t, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	})
	t.Run("new password too short", func(t *testing.T) {
		c.Post(t, path, map[string]string{"current_password": "TempPass123!", "new_password": "short"}).
			RequireError(t, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	})

	// Success returns 200 with a body (not 204), as FrontendDirections.md documents.
	var body map[string]string
	c.Post(t, path, map[string]string{"current_password": "TempPass123!", "new_password": "NewPass123!"}).
		RequireStatus(t, http.StatusOK).Data(t, &body)
	assert.Equal(t, map[string]string{"status": "password_changed"}, body)

	var me map[string]any
	c.Get(t, "/api/v1/auth/me").RequireStatus(t, http.StatusOK).Data(t, &me)
	assert.Equal(t, false, me["must_change_password"])

	refresh(t, srv, p.RefreshToken).RequireError(t, http.StatusUnauthorized, "UNAUTHORIZED") // all sessions revoked
	login(t, srv, u.Email, "TempPass123!").RequireError(t, http.StatusUnauthorized, "INVALID_CREDENTIALS")
	loginOK(t, srv, u.Email, "NewPass123!")
}

// A user with a temporary password may only read their profile and change
// the password; everything else is 403 PASSWORD_CHANGE_REQUIRED until then.
func TestAuth_MustChangePasswordIsEnforcedServerSide(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	room := srv.Factory.Classroom(t)

	stud := srv.AsStudent(t, testutil.MustChangePassword(), testutil.WithPassword("TempPass123!"))
	prof := srv.AsProfessor(t, testutil.MustChangePassword())
	admin := srv.AsAdmin(t, testutil.MustChangePassword())

	blocked := []struct {
		name         string
		client       *testutil.Client
		method, path string
		body         any
	}{
		{"student subjects", stud, "GET", "/api/v1/student/subjects", nil},
		{"student history", stud, "GET", "/api/v1/student/attendance", nil},
		{"student scan", stud, "POST", "/api/v1/attendance/scan", scanBody(room.Session.ID, "whatever-token")},
		{"professor subjects", prof, "GET", "/api/v1/subjects", nil},
		{"professor create subject", prof, "POST", "/api/v1/subjects", map[string]string{"name": "Blocked", "code": "BLK1"}},
		{"admin users", admin, "GET", "/api/v1/admin/users", nil},
		{"admin register", admin, "POST", "/api/v1/auth/register", map[string]string{"full_name": "X Y", "email": "x@example.test", "password": "TempPass123!", "role": "admin"}},
	}
	for _, tt := range blocked {
		t.Run("blocked: "+tt.name, func(t *testing.T) {
			tt.client.Do(t, tt.method, tt.path, tt.body).RequireError(t, http.StatusForbidden, "PASSWORD_CHANGE_REQUIRED")
		})
	}

	t.Run("blocked: websocket board", func(t *testing.T) {
		conn, hs, err := srv.DialWS(t, "/ws/sessions/"+room.Session.ID.String()+"/qr", prof.Token)
		require.Error(t, err)
		require.Nil(t, conn)
		assert.Equal(t, http.StatusForbidden, hs.StatusCode)
		assert.Contains(t, string(hs.Body), "PASSWORD_CHANGE_REQUIRED")
	})

	t.Run("allowed: profile shows the flag", func(t *testing.T) {
		me := stud.Get(t, "/api/v1/auth/me").RequireStatus(t, http.StatusOK).DataMap(t)
		assert.Equal(t, true, me["must_change_password"])
	})

	t.Run("the same access token works once the password is changed", func(t *testing.T) {
		stud.Post(t, "/api/v1/auth/change-password", map[string]string{"current_password": "TempPass123!", "new_password": "NewPass123!"}).
			RequireStatus(t, http.StatusOK)
		stud.Get(t, "/api/v1/student/subjects").RequireStatus(t, http.StatusOK)
	})
}

func TestAuth_Refresh(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	u := srv.Factory.User(t, models.RoleProfessor)

	t.Run("valid token rotates the pair", func(t *testing.T) {
		first := loginOK(t, srv, u.Email, testutil.DefaultPassword)
		res := refresh(t, srv, first.RefreshToken).RequireStatus(t, http.StatusOK)
		assertShape(t, tokenPairShape, res.DataMap(t))

		var second tokenPair
		res.Data(t, &second)
		assert.NotEqual(t, first.RefreshToken, second.RefreshToken)
		refresh(t, srv, second.RefreshToken).RequireStatus(t, http.StatusOK)
	})

	t.Run("reused token is rejected", func(t *testing.T) {
		first := loginOK(t, srv, u.Email, testutil.DefaultPassword)
		refresh(t, srv, first.RefreshToken).RequireStatus(t, http.StatusOK)
		refresh(t, srv, first.RefreshToken).RequireError(t, http.StatusUnauthorized, "UNAUTHORIZED")
	})

	t.Run("expired token is rejected", func(t *testing.T) {
		raw := "expired-refresh-token-" + u.ID.String()
		err := srv.App.Store.Refresh.Create(context.Background(), u.ID, auth.HashRefreshToken(raw), time.Now().Add(-time.Minute))
		require.NoError(t, err)
		refresh(t, srv, raw).RequireError(t, http.StatusUnauthorized, "UNAUTHORIZED")
	})

	t.Run("unknown token is rejected", func(t *testing.T) {
		refresh(t, srv, "never-issued").RequireError(t, http.StatusUnauthorized, "UNAUTHORIZED")
	})

	t.Run("deactivated user cannot refresh", func(t *testing.T) {
		v := srv.Factory.User(t, models.RoleStudent)
		p := loginOK(t, srv, v.Email, testutil.DefaultPassword)
		srv.AsAdmin(t).Patch(t, "/api/v1/admin/users/"+v.ID.String(), map[string]any{"is_active": false}).
			RequireStatus(t, http.StatusOK)
		refresh(t, srv, p.RefreshToken).RequireStatus(t, http.StatusUnauthorized)
	})
}

func TestAuth_RefreshTokenReuseRevokesFamily(t *testing.T) {
	testutil.KnownIssue(t, "(security) reusing an already-rotated refresh token is rejected, "+
		"but the tokens issued from it stay valid; reuse should revoke the whole family")
	t.Parallel()
	srv := testutil.NewServer(t)
	u := srv.Factory.User(t, models.RoleStudent)

	first := loginOK(t, srv, u.Email, testutil.DefaultPassword)
	var second tokenPair
	refresh(t, srv, first.RefreshToken).RequireStatus(t, http.StatusOK).Data(t, &second)

	refresh(t, srv, first.RefreshToken).RequireStatus(t, http.StatusUnauthorized) // theft detected
	refresh(t, srv, second.RefreshToken).RequireStatus(t, http.StatusUnauthorized)
}

func TestAuth_Logout(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	u := srv.Factory.User(t, models.RoleStudent)
	p := loginOK(t, srv, u.Email, testutil.DefaultPassword)

	var body map[string]string
	srv.Guest().Post(t, "/api/v1/auth/logout", map[string]string{"refresh_token": p.RefreshToken}).
		RequireStatus(t, http.StatusOK).Data(t, &body)
	assert.Equal(t, map[string]string{"status": "logged_out"}, body)

	refresh(t, srv, p.RefreshToken).RequireError(t, http.StatusUnauthorized, "UNAUTHORIZED")

	// Logging out again, or with an unknown token, is idempotent.
	srv.Guest().Post(t, "/api/v1/auth/logout", map[string]string{"refresh_token": p.RefreshToken}).RequireStatus(t, http.StatusOK)
	srv.Guest().Post(t, "/api/v1/auth/logout", map[string]string{"refresh_token": "unknown"}).RequireStatus(t, http.StatusOK)
	srv.Guest().Post(t, "/api/v1/auth/logout", map[string]string{}).RequireError(t, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
}

func TestAuth_Me(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	stud := srv.AsStudent(t)

	res := stud.Get(t, "/api/v1/auth/me").RequireStatus(t, http.StatusOK)
	me := res.DataMap(t)
	assertShape(t, userShape, me)
	assert.Equal(t, *stud.User.StudentCode, me["student_code"])
	assert.NotContains(t, string(res.Body), "password_hash")
}

// Access tokens stop working as soon as the account is deactivated or
// deleted, not only when they expire.
func TestAuth_DeactivationRevokesAccessTokens(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	admin := srv.AsAdmin(t)

	t.Run("deactivated", func(t *testing.T) {
		stud := srv.AsStudent(t)
		prof := srv.AsProfessor(t)
		sessionID := srv.Factory.LiveSession(t, srv.Factory.Subject(t, prof.User.ID).ID).ID.String()

		for _, u := range []*testutil.Client{stud, prof} {
			admin.Patch(t, "/api/v1/admin/users/"+u.User.ID.String(), map[string]any{"is_active": false}).
				RequireStatus(t, http.StatusOK)
		}
		stud.Get(t, "/api/v1/auth/me").RequireError(t, http.StatusUnauthorized, "UNAUTHORIZED")
		stud.Get(t, "/api/v1/student/subjects").RequireError(t, http.StatusUnauthorized, "UNAUTHORIZED")
		prof.Get(t, "/api/v1/subjects").RequireError(t, http.StatusUnauthorized, "UNAUTHORIZED")

		conn, hs, err := srv.DialWS(t, "/ws/sessions/"+sessionID+"/qr", prof.Token)
		require.Error(t, err)
		assert.Nil(t, conn)
		assert.Equal(t, http.StatusUnauthorized, hs.StatusCode)

		// Logging in again explains why.
		login(t, srv, stud.User.Email, testutil.DefaultPassword).RequireError(t, http.StatusForbidden, "ACCOUNT_INACTIVE")
	})

	t.Run("deleted", func(t *testing.T) {
		stud := srv.AsStudent(t)
		admin.Delete(t, "/api/v1/admin/users/"+stud.User.ID.String()).RequireStatus(t, http.StatusNoContent)
		stud.Get(t, "/api/v1/auth/me").RequireError(t, http.StatusUnauthorized, "UNAUTHORIZED")
	})

	t.Run("an admin cannot keep using a deactivated admin token", func(t *testing.T) {
		other := srv.AsAdmin(t)
		admin.Patch(t, "/api/v1/admin/users/"+other.User.ID.String(), map[string]any{"is_active": false}).
			RequireStatus(t, http.StatusOK)
		other.Get(t, "/api/v1/admin/users").RequireError(t, http.StatusUnauthorized, "UNAUTHORIZED")
	})
}

func TestAuth_ExpiredAccessToken(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)
	stud := srv.AsStudent(t)

	srv.Clock.Advance(31 * time.Minute) // JWT_ACCESS_TTL_MINUTES=30
	stud.Get(t, "/api/v1/auth/me").RequireError(t, http.StatusUnauthorized, "UNAUTHORIZED")
}
