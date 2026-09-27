package apperr

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithDetailsAndCauseDoNotMutateSharedErrors(t *testing.T) {
	cause := errors.New("boom")
	withDetails := NotFound.WithDetails("user not found")
	withCause := NotFound.WithCause(cause)

	assert.Nil(t, NotFound.Details, "package-level error must stay pristine")
	assert.NoError(t, NotFound.Unwrap())
	assert.Equal(t, "user not found", withDetails.Details)
	assert.ErrorIs(t, withCause, cause)
	assert.Equal(t, NotFound.Code, withDetails.Code)
	assert.Equal(t, NotFound.Status, withCause.Status)
}

func TestErrorString(t *testing.T) {
	assert.Equal(t, "NOT_FOUND: resource not found", NotFound.Error())
	assert.Equal(t, "INTERNAL: an internal error occurred: boom", Internal.WithCause(errors.New("boom")).Error())
}

func TestAs(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode string
	}{
		{"domain error", Forbidden, "FORBIDDEN"},
		{"wrapped domain error", fmt.Errorf("layer: %w", TokenExpired), "TOKEN_EXPIRED"},
		{"plain error becomes internal", errors.New("x"), "INTERNAL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantCode, As(tt.err).Code)
		})
	}

	plain := errors.New("x")
	assert.ErrorIs(t, As(plain), plain, "the original error is kept as the cause")
}

func TestNew(t *testing.T) {
	e := New(418, "TEAPOT", "short and stout")
	assert.Equal(t, 418, e.Status)
	assert.Equal(t, "TEAPOT", e.Code)
	assert.Equal(t, "short and stout", e.Message)
}
