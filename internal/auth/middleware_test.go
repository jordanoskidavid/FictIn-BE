package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"fictin/internal/clock"
	"fictin/internal/models"
)

// errorCode extracts error.code from a JSON error envelope.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), rec.Body.String())
	return env.Error.Code
}

func TestAuthRequired(t *testing.T) {
	clk := clock.NewFake(t0)
	tm := NewTokenManager("secret", time.Minute, time.Hour, WithClock(clk))
	mw := NewMiddleware(tm)

	user := testUser(models.RoleProfessor)
	valid, _, err := tm.GenerateAccessToken(user)
	require.NoError(t, err)
	other, _, err := NewTokenManager("other", time.Minute, time.Hour, WithClock(clk)).GenerateAccessToken(user)
	require.NoError(t, err)

	tests := []struct {
		name       string
		header     string
		query      string
		wantStatus int
	}{
		{"valid bearer header", "Bearer " + valid, "", http.StatusOK},
		{"scheme is case insensitive", "bearer " + valid, "", http.StatusOK},
		{"query param (websocket handshake)", "", valid, http.StatusOK},
		{"no credentials", "", "", http.StatusUnauthorized},
		{"wrong scheme", "Basic " + valid, "", http.StatusUnauthorized},
		{"bearer without token", "Bearer ", "", http.StatusUnauthorized},
		{"token signed with another secret", "Bearer " + other, "", http.StatusUnauthorized},
		{"garbage token", "Bearer abc.def.ghi", "", http.StatusUnauthorized},
		{"invalid header wins over valid query", "Basic x", valid, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Identity
			h := mw.AuthRequired(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = MustIdentity(r.Context())
				w.WriteHeader(http.StatusOK)
			}))
			target := "/x"
			if tt.query != "" {
				target += "?access_token=" + tt.query
			}
			req := httptest.NewRequest(http.MethodGet, target, nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantStatus == http.StatusOK {
				assert.Equal(t, Identity{UserID: user.ID, Role: user.Role, Email: user.Email}, got)
			} else {
				assert.Equal(t, "UNAUTHORIZED", errorCode(t, rec))
			}
		})
	}

	t.Run("expired token", func(t *testing.T) {
		clk := clock.NewFake(t0)
		tm := NewTokenManager("secret", time.Minute, time.Hour, WithClock(clk))
		tok, _, err := tm.GenerateAccessToken(user)
		require.NoError(t, err)
		clk.Advance(2 * time.Minute)

		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		NewMiddleware(tm).AuthRequired(http.NotFoundHandler()).ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

func TestRequireRole(t *testing.T) {
	mw := NewMiddleware(NewTokenManager("s", time.Minute, time.Hour))
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	tests := []struct {
		name       string
		allowed    []models.Role
		caller     *models.Role // nil = no identity in context
		wantStatus int
		wantCode   string
	}{
		{"admin allowed", []models.Role{models.RoleAdmin}, ptr(models.RoleAdmin), http.StatusOK, ""},
		{"student denied admin route", []models.Role{models.RoleAdmin}, ptr(models.RoleStudent), http.StatusForbidden, "FORBIDDEN"},
		{"professor denied admin route", []models.Role{models.RoleAdmin}, ptr(models.RoleProfessor), http.StatusForbidden, "FORBIDDEN"},
		{"one of several allowed", []models.Role{models.RoleProfessor, models.RoleAdmin}, ptr(models.RoleAdmin), http.StatusOK, ""},
		{"unknown role denied", []models.Role{models.RoleStudent}, ptr(models.Role("superuser")), http.StatusForbidden, "FORBIDDEN"},
		{"no identity is unauthorized", []models.Role{models.RoleStudent}, nil, http.StatusUnauthorized, "UNAUTHORIZED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			if tt.caller != nil {
				req = req.WithContext(WithIdentity(req.Context(), Identity{Role: *tt.caller}))
			}
			rec := httptest.NewRecorder()
			mw.RequireRole(tt.allowed...)(ok).ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantCode != "" {
				assert.Equal(t, tt.wantCode, errorCode(t, rec))
			}
		})
	}
}

func TestIdentityHelpers(t *testing.T) {
	assert.True(t, Identity{Role: models.RoleAdmin}.IsAdmin())
	assert.True(t, Identity{Role: models.RoleProfessor}.IsProfessor())
	assert.True(t, Identity{Role: models.RoleStudent}.IsStudent())
	assert.False(t, Identity{Role: models.RoleStudent}.IsAdmin())

	_, ok := FromContext(t.Context())
	assert.False(t, ok)
	assert.Panics(t, func() { MustIdentity(t.Context()) })
}

func ptr[T any](v T) *T { return &v }
