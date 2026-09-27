package httpresponse

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"fictin/internal/apperr"
)

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var env map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), rec.Body.String())
	return env
}

func TestSuccessEnvelope(t *testing.T) {
	tests := []struct {
		name       string
		write      func(w http.ResponseWriter)
		wantStatus int
		wantData   string
	}{
		{"OK wraps object", func(w http.ResponseWriter) { OK(w, map[string]int{"n": 1}) }, 200, `{"n":1}`},
		{"Created wraps object", func(w http.ResponseWriter) { Created(w, map[string]string{"id": "x"}) }, 201, `{"id":"x"}`},
		{"empty list stays a list", func(w http.ResponseWriter) { OK(w, []int{}) }, 200, `[]`},
		{"JSON with custom status", func(w http.ResponseWriter) { JSON(w, http.StatusAccepted, "queued") }, 202, `"queued"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.write(rec)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
			env := decodeBody(t, rec)
			assert.Len(t, env, 1, "only the data key is present")
			assert.JSONEq(t, tt.wantData, string(env["data"]))
		})
	}
}

// Documents current behavior: a nil slice is rendered as {"data": null}, not
// []. Repositories build lists with `var out []T`, so empty list endpoints
// return null although FrontendDirections.md promises an array. Reported as a
// contract mismatch; change this test if the backend starts returning [].
func TestNilSliceRendersAsNull(t *testing.T) {
	rec := httptest.NewRecorder()
	var rows []string
	OK(rec, rows)
	assert.JSONEq(t, `{"data":null}`, rec.Body.String())
}

func TestNoContent(t *testing.T) {
	rec := httptest.NewRecorder()
	NoContent(rec)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
}

func TestFailEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	FailWithDetails(rec, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "bad", map[string]string{"email": "required"})

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.JSONEq(t, `{"error":{"code":"VALIDATION_ERROR","message":"bad","details":{"email":"required"}}}`, rec.Body.String())

	rec = httptest.NewRecorder()
	Fail(rec, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "down")
	assert.JSONEq(t, `{"error":{"code":"DB_UNAVAILABLE","message":"down"}}`, rec.Body.String(), "details omitted when empty")
}

// Every domain error the frontend branches on must map to a stable status + code.
func TestFromErrorMapsDomainErrors(t *testing.T) {
	tests := []struct {
		err        *apperr.Error
		wantStatus int
		wantCode   string
	}{
		{apperr.Internal, 500, "INTERNAL"},
		{apperr.Unauthorized, 401, "UNAUTHORIZED"},
		{apperr.Forbidden, 403, "FORBIDDEN"},
		{apperr.NotFound, 404, "NOT_FOUND"},
		{apperr.Conflict, 409, "CONFLICT"},
		{apperr.Validation, 422, "VALIDATION_ERROR"},
		{apperr.BadRequest, 400, "BAD_REQUEST"},
		{apperr.RateLimited, 429, "RATE_LIMITED"},
		{apperr.InvalidCreds, 401, "INVALID_CREDENTIALS"},
		{apperr.AccountInactive, 403, "ACCOUNT_INACTIVE"},
		{apperr.TokenExpired, 410, "TOKEN_EXPIRED"},
		{apperr.TokenInvalid, 400, "TOKEN_INVALID"},
		{apperr.SessionNotLive, 409, "SESSION_NOT_LIVE"},
		{apperr.NotEnrolled, 403, "NOT_ENROLLED"},
		{apperr.AlreadyMarked, 200, "ALREADY_MARKED"},
	}
	for _, tt := range tests {
		t.Run(tt.wantCode, func(t *testing.T) {
			rec := httptest.NewRecorder()
			FromError(rec, httptest.NewRequest(http.MethodGet, "/", nil), tt.err)

			assert.Equal(t, tt.wantStatus, rec.Code)
			var env struct {
				Error Error `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
			assert.Equal(t, tt.wantCode, env.Error.Code)
			assert.Equal(t, tt.err.Message, env.Error.Message)
		})
	}
}

func TestFromErrorEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantDetails string
	}{
		{"plain error becomes 500 without leaking its text", errors.New("pq: secret table missing"), 500, "INTERNAL", ""},
		{"wrapped domain error keeps its code", fmt.Errorf("ctx: %w", apperr.NotFound), 404, "NOT_FOUND", ""},
		{"details are rendered", apperr.Conflict.WithDetails("email already in use"), 409, "CONFLICT", `"email already in use"`},
		{"cause is not rendered", apperr.Internal.WithCause(errors.New("db down")), 500, "INTERNAL", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			FromError(rec, httptest.NewRequest(http.MethodGet, "/", nil), tt.err)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.NotContains(t, rec.Body.String(), "secret table")
			assert.NotContains(t, rec.Body.String(), "db down")
			var env struct {
				Error struct {
					Code    string          `json:"code"`
					Details json.RawMessage `json:"details"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
			assert.Equal(t, tt.wantCode, env.Error.Code)
			if tt.wantDetails == "" {
				assert.Empty(t, env.Error.Details)
			} else {
				assert.JSONEq(t, tt.wantDetails, string(env.Error.Details))
			}
		})
	}
}
