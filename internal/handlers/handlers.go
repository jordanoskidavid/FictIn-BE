// Package handlers contains the HTTP layer: request decoding, validation,
// delegating to services, and response encoding.
package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"fictin/internal/apperr"
	"fictin/internal/httpresponse"
)

var validate = validator.New(validator.WithRequiredStructEnabled())

// decode reads and validates a JSON body into dst (a pointer to a struct).
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			httpresponse.FromError(w, r, apperr.BadRequest.WithDetails("request body too large"))
		case errors.Is(err, io.EOF):
			httpresponse.FromError(w, r, apperr.BadRequest.WithDetails("request body is empty"))
		default:
			httpresponse.FromError(w, r, apperr.BadRequest.WithDetails(err.Error()))
		}
		return false
	}
	if err := validate.Struct(dst); err != nil {
		httpresponse.FromError(w, r, validationDetails(err))
		return false
	}
	return true
}

func validationDetails(err error) error {
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return apperr.Validation.WithCause(err)
	}
	fields := make(map[string]string, len(verrs))
	for _, fe := range verrs {
		fields[toSnake(fe.Field())] = fe.Tag()
	}
	return apperr.Validation.WithDetails(fields)
}

// uuidParam extracts and parses a chi URL param as a UUID.
func uuidParam(r *http.Request, name string) (uuid.UUID, error) {
	raw := chi.URLParam(r, name)
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, apperr.BadRequest.WithDetails(name + " must be a valid UUID")
	}
	return id, nil
}

func queryInt(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func optionalUUIDQuery(r *http.Request, name string) (*uuid.UUID, error) {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		return nil, nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return nil, apperr.BadRequest.WithDetails(name + " must be a valid UUID")
	}
	return &id, nil
}

func toSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
