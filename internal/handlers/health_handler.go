package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fictin/internal/httpresponse"
)

type HealthHandler struct {
	pool *pgxpool.Pool
}

func NewHealthHandler(pool *pgxpool.Pool) *HealthHandler {
	return &HealthHandler{pool: pool}
}

// Live is a pure liveness probe.
func (h *HealthHandler) Live(w http.ResponseWriter, r *http.Request) {
	httpresponse.OK(w, map[string]string{"status": "ok"})
}

// Ready checks the DB connection.
func (h *HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.pool.Ping(ctx); err != nil {
		httpresponse.Fail(w, http.StatusServiceUnavailable, "DB_UNAVAILABLE", "database not reachable")
		return
	}
	httpresponse.OK(w, map[string]string{"status": "ready"})
}
