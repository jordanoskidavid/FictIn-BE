//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"fictin/internal/testutil"
)

func TestSmoke_HealthAndReadiness(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)

	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			var body map[string]string
			srv.Guest().Get(t, path).RequireStatus(t, http.StatusOK).Data(t, &body)
			assert.NotEmpty(t, body["status"])
		})
	}
}

func TestSmoke_RoleClientsAreAuthenticated(t *testing.T) {
	t.Parallel()
	srv := testutil.NewServer(t)

	for _, c := range []*testutil.Client{srv.AsAdmin(t), srv.AsProfessor(t), srv.AsStudent(t)} {
		t.Run(string(c.User.Role), func(t *testing.T) {
			var me struct {
				ID   string `json:"id"`
				Role string `json:"role"`
			}
			c.Get(t, "/api/v1/auth/me").RequireStatus(t, http.StatusOK).Data(t, &me)
			assert.Equal(t, c.User.ID.String(), me.ID)
			assert.Equal(t, string(c.User.Role), me.Role)
		})
	}
}
