//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"fictin/internal/config"
	"fictin/internal/testutil"
)

// The request timeout (HTTP_REQUEST_TIMEOUT_SECONDS, 30s in production) must
// not cut off the professor's QR board, which streams for the whole class.
// The timeout is shrunk to 200ms so the test only needs ~1s of streaming.
func TestWS_BoardOutlivesRequestTimeout(t *testing.T) {
	t.Parallel()
	const timeout = 200 * time.Millisecond
	srv := testutil.NewServer(t, testutil.WithConfig(func(c *config.Config) {
		c.RequestTimeout = timeout
		c.QRRotationInterval = 50 * time.Millisecond
	}))
	prof := srv.AsProfessor(t)
	subj := srv.Factory.Subject(t, prof.User.ID)

	var sess struct {
		ID string `json:"id"`
	}
	prof.Post(t, "/api/v1/sessions/"+subj.ID.String()+"/start", nil).RequireStatus(t, http.StatusCreated).Data(t, &sess)

	conn, res, err := srv.DialWS(t, "/ws/sessions/"+sess.ID+"/qr", prof.Token)
	require.NoError(t, err, "handshake failed: %+v", res)

	// Keep reading well past 5x the timeout: every read must succeed.
	deadline := time.Now().Add(5 * timeout)
	received := 0
	for time.Now().Before(deadline) {
		_, err := testutil.ReadJSON[map[string]any](t, conn, 2*time.Second)
		require.NoError(t, err, "board disconnected after %d tokens (request timeout %s)", received, timeout)
		received++
	}
	require.Greater(t, received, 5)
}
