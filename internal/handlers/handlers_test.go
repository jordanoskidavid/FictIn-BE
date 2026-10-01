package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type errBody struct {
	Error struct {
		Code    string          `json:"code"`
		Details json.RawMessage `json:"details"`
	} `json:"error"`
}

func decodeInto(t *testing.T, body string, dst any) (bool, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rec := httptest.NewRecorder()
	return decode(rec, req, dst), rec
}

func TestDecode(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantOK      bool
		wantStatus  int
		wantCode    string
		wantDetails string // JSON; checked when non-empty
	}{
		{"valid", `{"email":"a@b.co","password":"x"}`, true, 0, "", ""},
		{"empty body", ``, false, 400, "BAD_REQUEST", `"request body is empty"`},
		{"malformed json", `{"email":`, false, 400, "BAD_REQUEST", ""},
		{"unknown field rejected", `{"email":"a@b.co","password":"x","admin":true}`, false, 400, "BAD_REQUEST", ""},
		{"wrong type", `{"email":1,"password":"x"}`, false, 400, "BAD_REQUEST", ""},
		{"missing required", `{"email":"a@b.co"}`, false, 422, "VALIDATION_ERROR", `{"password":"required"}`},
		{"invalid email", `{"email":"nope","password":"x"}`, false, 422, "VALIDATION_ERROR", `{"email":"email"}`},
		{"body too large", `{"email":"` + strings.Repeat("a", 1<<20) + `","password":"x"}`, false, 400, "BAD_REQUEST", `"request body too large"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var dst loginRequest
			ok, rec := decodeInto(t, tt.body, &dst)
			require.Equal(t, tt.wantOK, ok, rec.Body.String())
			if tt.wantOK {
				return
			}
			assert.Equal(t, tt.wantStatus, rec.Code)
			var body errBody
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, tt.wantCode, body.Error.Code)
			if tt.wantDetails != "" {
				assert.JSONEq(t, tt.wantDetails, string(body.Error.Details))
			}
		})
	}
}

// Validation rules on request structs, reported as snake_case field -> tag.
func TestRequestValidation(t *testing.T) {
	long := func(n int) string { return strings.Repeat("x", n) }
	tests := []struct {
		name        string
		dst         any
		body        string
		wantDetails string // "" = valid
	}{
		{"register ok", &registerRequest{}, `{"full_name":"Jane Doe","email":"j@x.co","password":"12345678","role":"student","student_code":"S1"}`, ""},
		{"register short password", &registerRequest{}, `{"full_name":"Jane Doe","email":"j@x.co","password":"1234567","role":"student"}`, `{"password":"min"}`},
		{"register unknown role", &registerRequest{}, `{"full_name":"Jane Doe","email":"j@x.co","password":"12345678","role":"root"}`, `{"role":"oneof"}`},
		{"register name too short", &registerRequest{}, `{"full_name":"J","email":"j@x.co","password":"12345678","role":"admin"}`, `{"full_name":"min"}`},
		{"register student code too long", &registerRequest{}, `{"full_name":"Jane","email":"j@x.co","password":"12345678","role":"student","student_code":"` + long(51) + `"}`, `{"student_code":"max"}`},
		{"change password too short", &changePasswordRequest{}, `{"current_password":"a","new_password":"short"}`, `{"new_password":"min"}`},
		{"scan ok", &scanRequest{}, `{"session_id":"` + uuid.NewString() + `","token":"abcdefgh"}`, ""},
		{"scan bad session id", &scanRequest{}, `{"session_id":"123","token":"abcdefgh"}`, `{"session_id":"uuid"}`},
		{"scan token too short", &scanRequest{}, `{"session_id":"` + uuid.NewString() + `","token":"abc"}`, `{"token":"min"}`},
		{"scan token too long", &scanRequest{}, `{"session_id":"` + uuid.NewString() + `","token":"` + long(129) + `"}`, `{"token":"max"}`},
		{"subject ok", &createSubjectRequest{}, `{"name":"Databases","code":"CS101"}`, ""},
		{"subject code too long", &createSubjectRequest{}, `{"name":"Databases","code":"` + long(31) + `"}`, `{"code":"max"}`},
		{"schedule day out of range", &createScheduleRequest{}, `{"day_of_week":7,"start_time":"10:00","end_time":"11:00"}`, `{"day_of_week":"max"}`},
		{"enroll bad student id", &enrollRequest{}, `{"student_id":"nope"}`, `{"student_id":"uuid"}`},
		{"update user name too short", &updateUserRequest{}, `{"full_name":"A"}`, `{"full_name":"min"}`},
		{"start session bad schedule id", &startSessionRequest{}, `{"schedule_id":"nope"}`, `{"schedule_id":"uuid"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, rec := decodeInto(t, tt.body, tt.dst)
			if tt.wantDetails == "" {
				assert.True(t, ok, rec.Body.String())
				return
			}
			require.False(t, ok)
			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
			var body errBody
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.JSONEq(t, tt.wantDetails, string(body.Error.Details))
		})
	}
}

func TestUUIDParam(t *testing.T) {
	id := uuid.New()
	withParam := func(v string) *http.Request {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", v)
		return httptest.NewRequest(http.MethodGet, "/", nil).WithContext(context.WithValue(context.Background(), chi.RouteCtxKey, rctx))
	}

	got, err := uuidParam(withParam(id.String()), "id")
	require.NoError(t, err)
	assert.Equal(t, id, got)

	_, err = uuidParam(withParam("not-a-uuid"), "id")
	assert.ErrorContains(t, err, "BAD_REQUEST")
}

func TestQueryHelpers(t *testing.T) {
	id := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/?limit=10&bad=x&sid="+id.String()+"&bogus=nope&blank=%20", nil)

	assert.Equal(t, 10, queryInt(req, "limit", 50))
	assert.Equal(t, 50, queryInt(req, "missing", 50))
	assert.Equal(t, 50, queryInt(req, "bad", 50), "non-numeric falls back to the default")

	got, err := optionalUUIDQuery(req, "sid")
	require.NoError(t, err)
	assert.Equal(t, id, *got)

	got, err = optionalUUIDQuery(req, "blank")
	require.NoError(t, err)
	assert.Nil(t, got)

	_, err = optionalUUIDQuery(req, "bogus")
	assert.ErrorContains(t, err, "BAD_REQUEST")
}
