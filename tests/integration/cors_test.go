//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"fictin/internal/config"
	"fictin/internal/testutil"
)

const allowedOrigin = "https://app.example.test"

// rawResponse is the part of a response the CORS tests look at.
type rawResponse struct {
	StatusCode int
	Header     http.Header
}

func rawRequest(t *testing.T, srv *testutil.Server, method, path, origin string) rawResponse {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	require.NoError(t, err)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if method == http.MethodOptions {
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	}
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	_ = res.Body.Close()
	return rawResponse{StatusCode: res.StatusCode, Header: res.Header}
}

func TestCORS_RestrictedOrigins(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t, testutil.WithConfig(func(c *config.Config) {
		c.CORSAllowedOrigins = []string{allowedOrigin, "https://admin.example.test"}
	}))

	tests := []struct {
		name       string
		method     string
		origin     string
		wantStatus int
		wantACAO   string
	}{
		{"allowed origin is echoed", http.MethodGet, allowedOrigin, http.StatusOK, allowedOrigin},
		{"second allowed origin", http.MethodGet, "https://admin.example.test", http.StatusOK, "https://admin.example.test"},
		{"unknown origin gets no allow header", http.MethodGet, "https://evil.example", http.StatusOK, ""},
		{"no origin (same-origin / curl)", http.MethodGet, "", http.StatusOK, ""},
		{"preflight from allowed origin", http.MethodOptions, allowedOrigin, http.StatusNoContent, allowedOrigin},
		{"preflight from unknown origin", http.MethodOptions, "https://evil.example", http.StatusNoContent, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := rawRequest(t, srv, tt.method, "/healthz", tt.origin)
			assert.Equal(t, tt.wantStatus, res.StatusCode)
			assert.Equal(t, tt.wantACAO, res.Header.Get("Access-Control-Allow-Origin"))
			if tt.wantACAO != "" {
				assert.Contains(t, res.Header.Values("Vary"), "Origin")
				assert.Equal(t, "GET, POST, PATCH, DELETE, OPTIONS", res.Header.Get("Access-Control-Allow-Methods"))
				assert.Equal(t, "Authorization, Content-Type", res.Header.Get("Access-Control-Allow-Headers"))
			}
		})
	}
}

func TestCORS_WildcardForLocalDevelopment(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t) // .env.test: CORS_ALLOWED_ORIGINS=*

	res := rawRequest(t, srv, http.MethodGet, "/healthz", "http://localhost:5173")
	assert.Equal(t, "*", res.Header.Get("Access-Control-Allow-Origin"))
}

// The WebSocket handshake enforces the same origin list (browsers do not
// apply CORS to WebSockets, so the server must).
func TestCORS_WebSocketOriginCheck(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t, testutil.WithConfig(func(c *config.Config) {
		c.CORSAllowedOrigins = []string{allowedOrigin}
	}))
	prof := srv.AsProfessor(t)
	sessionID := startSession(t, prof, srv.Factory.Subject(t, prof.User.ID).ID)
	path := "/ws/sessions/" + sessionID + "/qr?access_token=" + prof.Token

	conn, hs, err := srv.DialWSWithHeader(t, path, http.Header{"Origin": {"https://evil.example"}})
	require.Error(t, err)
	assert.Nil(t, conn)
	assert.Equal(t, http.StatusForbidden, hs.StatusCode)

	conn, hs, err = srv.DialWSWithHeader(t, path, http.Header{"Origin": {allowedOrigin}})
	require.NoError(t, err, "handshake: %d %s", hs.StatusCode, hs.Body)
	require.NotNil(t, conn)
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)

	for _, path := range []string{"/healthz", "/api/v1/auth/me", "/api/v1/does-not-exist"} {
		t.Run(path, func(t *testing.T) {
			res := rawRequest(t, srv, http.MethodGet, path, "")
			assert.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
			assert.Equal(t, "DENY", res.Header.Get("X-Frame-Options"))
			assert.Equal(t, "no-referrer", res.Header.Get("Referrer-Policy"))
		})
	}
}
