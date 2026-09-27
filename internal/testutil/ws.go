package testutil

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// Handshake is the HTTP response to a WebSocket upgrade request, fully read.
type Handshake struct {
	StatusCode int
	Body       []byte // the JSON error envelope when the upgrade is rejected
}

// DialWS opens a WebSocket to path on the test server, authenticating the
// way browsers do: ?access_token=<token> (omitted when token is ""). On a
// rejected handshake conn is nil and hs carries the HTTP status and body.
func (s *Server) DialWS(t testing.TB, path, token string) (conn *websocket.Conn, hs Handshake, err error) {
	t.Helper()
	if token != "" {
		path += "?access_token=" + url.QueryEscape(token)
	}
	return s.DialWSWithHeader(t, path, nil)
}

// DialWSWithHeader is DialWS with explicit handshake headers (non-browser clients).
func (s *Server) DialWSWithHeader(t testing.TB, path string, header http.Header) (conn *websocket.Conn, hs Handshake, err error) {
	t.Helper()
	u := "ws" + strings.TrimPrefix(s.URL, "http") + path
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, res, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPClient: s.Client(), HTTPHeader: header})
	if res != nil {
		hs.StatusCode = res.StatusCode
		if res.Body != nil {
			hs.Body, _ = io.ReadAll(res.Body)
			_ = res.Body.Close()
		}
	}
	if conn != nil {
		t.Cleanup(func() { _ = conn.CloseNow() })
	}
	return conn, hs, err
}

// ReadJSON reads one JSON message, returning an error if none arrives in time.
func ReadJSON[T any](t testing.TB, conn *websocket.Conn, timeout time.Duration) (T, error) {
	t.Helper()
	var v T
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := wsjson.Read(ctx, conn, &v)
	return v, err
}
