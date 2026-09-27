package testutil

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"fictin/internal/models"
)

// Client sends requests to a Server, optionally authenticated as one user.
type Client struct {
	srv   *Server
	User  *models.User // nil for a guest
	Token string       // access token; "" for a guest
}

// Guest returns an unauthenticated client.
func (s *Server) Guest() *Client { return &Client{srv: s} }

// AsAdmin, AsProfessor and AsStudent create a fresh user with that role and
// return a client carrying a valid access token for it.
func (s *Server) AsAdmin(t testing.TB, opts ...UserOpt) *Client {
	return s.As(t, models.RoleAdmin, opts...)
}

func (s *Server) AsProfessor(t testing.TB, opts ...UserOpt) *Client {
	return s.As(t, models.RoleProfessor, opts...)
}

func (s *Server) AsStudent(t testing.TB, opts ...UserOpt) *Client {
	return s.As(t, models.RoleStudent, opts...)
}

// As creates a user with the given role and returns a client for it.
func (s *Server) As(t testing.TB, role models.Role, opts ...UserOpt) *Client {
	t.Helper()
	u := s.Factory.User(t, role, opts...)
	return s.ClientFor(t, u)
}

// ClientFor returns a client authenticated as an existing user.
func (s *Server) ClientFor(t testing.TB, u *models.User) *Client {
	t.Helper()
	tok, _, err := s.App.Tokens.GenerateAccessToken(u)
	if err != nil {
		t.Fatalf("issue access token: %v", err)
	}
	return &Client{srv: s, User: u, Token: tok}
}

func (c *Client) Get(t testing.TB, path string) *Response {
	t.Helper()
	return c.Do(t, http.MethodGet, path, nil)
}

func (c *Client) Post(t testing.TB, path string, body any) *Response {
	t.Helper()
	return c.Do(t, http.MethodPost, path, body)
}

func (c *Client) Patch(t testing.TB, path string, body any) *Response {
	t.Helper()
	return c.Do(t, http.MethodPatch, path, body)
}

func (c *Client) Delete(t testing.TB, path string) *Response {
	t.Helper()
	return c.Do(t, http.MethodDelete, path, nil)
}

// Do sends a request. body may be nil, a string/[]byte (sent verbatim) or any
// value (JSON-encoded).
func (c *Client) Do(t testing.TB, method, path string, body any) *Response {
	t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = strings.NewReader(b)
	case []byte:
		r = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("encode request body: %v", err)
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.srv.URL+path, r)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return &Response{Status: res.StatusCode, Header: res.Header, Body: raw, method: method, path: path}
}

// Response is a fully-read HTTP response.
type Response struct {
	Status int
	Header http.Header
	Body   []byte

	method, path string
}

// APIError is the {"error": {...}} envelope body.
type APIError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details,omitempty"`
}

// RequireStatus fails the test with the response body if the status differs.
func (r *Response) RequireStatus(t testing.TB, want int) *Response {
	t.Helper()
	if r.Status != want {
		t.Fatalf("%s %s: status = %d, want %d; body: %s", r.method, r.path, r.Status, want, r.Body)
	}
	return r
}

// Data decodes the {"data": ...} envelope into v, failing on anything else.
// Unknown top-level keys are rejected so envelope drift is caught.
func (r *Response) Data(t testing.TB, v any) {
	t.Helper()
	var env map[string]json.RawMessage
	if err := json.Unmarshal(r.Body, &env); err != nil {
		t.Fatalf("%s %s: response is not a JSON object: %v; body: %s", r.method, r.path, err, r.Body)
	}
	data, ok := env["data"]
	if !ok || len(env) != 1 {
		t.Fatalf("%s %s: want exactly {\"data\": ...}, got: %s", r.method, r.path, r.Body)
	}
	if v != nil {
		if err := json.Unmarshal(data, v); err != nil {
			t.Fatalf("%s %s: decode data: %v; body: %s", r.method, r.path, err, r.Body)
		}
	}
}

// DataMap decodes the data envelope into a generic map, handy for asserting
// the exact set of JSON field names the frontend relies on.
func (r *Response) DataMap(t testing.TB) map[string]any {
	t.Helper()
	var m map[string]any
	r.Data(t, &m)
	return m
}

// Error decodes the {"error": {...}} envelope, failing on anything else.
func (r *Response) Error(t testing.TB) APIError {
	t.Helper()
	var env map[string]json.RawMessage
	if err := json.Unmarshal(r.Body, &env); err != nil {
		t.Fatalf("%s %s: response is not a JSON object: %v; body: %s", r.method, r.path, err, r.Body)
	}
	raw, ok := env["error"]
	if !ok || len(env) != 1 {
		t.Fatalf("%s %s: want exactly {\"error\": ...}, got: %s", r.method, r.path, r.Body)
	}
	var e APIError
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("%s %s: decode error: %v", r.method, r.path, err)
	}
	if e.Code == "" || e.Message == "" {
		t.Fatalf("%s %s: error envelope missing code or message: %s", r.method, r.path, r.Body)
	}
	return e
}

// RequireError asserts status and error code in one step.
func (r *Response) RequireError(t testing.TB, status int, code string) APIError {
	t.Helper()
	r.RequireStatus(t, status)
	e := r.Error(t)
	if e.Code != code {
		t.Fatalf("%s %s: error code = %q, want %q; body: %s", r.method, r.path, e.Code, code, r.Body)
	}
	return e
}
