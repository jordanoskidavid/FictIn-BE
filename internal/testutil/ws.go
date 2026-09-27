package testutil

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// DialWS opens a WebSocket to path on the test server, authenticating the
// way browsers do: ?access_token=<token> (omitted when token is ""). On a
// rejected handshake conn is nil and res carries the HTTP status and body.
func (s *Server) DialWS(t testing.TB, path, token string) (conn *websocket.Conn, res *http.Response, err error) {
	t.Helper()
	u := "ws" + strings.TrimPrefix(s.URL, "http") + path
	if token != "" {
		u += "?access_token=" + url.QueryEscape(token)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, res, err = websocket.Dial(ctx, u, &websocket.DialOptions{HTTPClient: s.Client()})
	if conn != nil {
		t.Cleanup(func() { _ = conn.CloseNow() })
	}
	return conn, res, err
}

// ReadJSON reads one JSON message, failing the test if none arrives in time.
func ReadJSON[T any](t testing.TB, conn *websocket.Conn, timeout time.Duration) (T, error) {
	t.Helper()
	var v T
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := wsjson.Read(ctx, conn, &v)
	return v, err
}
