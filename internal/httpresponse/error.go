package httpresponse

import (
	"log/slog"
	"net/http"

	"fictin/internal/apperr"
)

// FromError renders any error as a JSON error body. Domain (*apperr.Error)
// values keep their code/status/message; anything else becomes a 500.
func FromError(w http.ResponseWriter, r *http.Request, err error) {
	e := apperr.As(err)

	if e.Status >= 500 {
		slog.ErrorContext(r.Context(), "request failed",
			"err", err, "code", e.Code, "method", r.Method, "path", r.URL.Path)
	}

	// ALREADY_MARKED is a 200 "success-ish" outcome per spec; callers that want a
	// body should handle it explicitly, but if it reaches here treat it as data.
	FailWithDetails(w, e.Status, e.Code, e.Message, e.Details)
}
