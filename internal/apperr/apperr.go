// Package apperr defines a transport-agnostic error type carried from the
// service layer up to HTTP handlers, where it is rendered as a JSON error body.
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// Error is a domain error with a stable machine code and an HTTP status.
type Error struct {
	Status  int
	Code    string
	Message string
	Details any
	err     error // wrapped cause, not exposed to clients
}

func (e *Error) Error() string {
	if e.err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.err }

// WithCause attaches an internal cause for logging without changing the client-facing message.
func (e *Error) WithCause(err error) *Error {
	clone := *e
	clone.err = err
	return &clone
}

// WithDetails attaches a structured details payload.
func (e *Error) WithDetails(details any) *Error {
	clone := *e
	clone.Details = details
	return &clone
}

// New builds an ad-hoc error.
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// As extracts an *Error from an error chain, or synthesizes a 500 if none is present.
func As(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Internal.WithCause(err)
}

// Common reusable errors.
var (
	Internal               = New(http.StatusInternalServerError, "INTERNAL", "an internal error occurred")
	Unauthorized           = New(http.StatusUnauthorized, "UNAUTHORIZED", "authentication required or invalid")
	Forbidden              = New(http.StatusForbidden, "FORBIDDEN", "you do not have permission to perform this action")
	NotFound               = New(http.StatusNotFound, "NOT_FOUND", "resource not found")
	Conflict               = New(http.StatusConflict, "CONFLICT", "resource conflict")
	Validation             = New(http.StatusUnprocessableEntity, "VALIDATION_ERROR", "request validation failed")
	BadRequest             = New(http.StatusBadRequest, "BAD_REQUEST", "malformed request")
	RateLimited            = New(http.StatusTooManyRequests, "RATE_LIMITED", "too many requests, slow down")
	InvalidCreds           = New(http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid email or password")
	AccountInactive        = New(http.StatusForbidden, "ACCOUNT_INACTIVE", "this account has been deactivated")
	PasswordChangeRequired = New(http.StatusForbidden, "PASSWORD_CHANGE_REQUIRED",
		"change your temporary password before continuing")

	// Attendance-scan specific (spec §7.3).
	TokenExpired   = New(http.StatusGone, "TOKEN_EXPIRED", "the QR token has expired")
	TokenInvalid   = New(http.StatusBadRequest, "TOKEN_INVALID", "the QR token is invalid for this session")
	SessionNotLive = New(http.StatusConflict, "SESSION_NOT_LIVE", "the session is not currently live")
	NotEnrolled    = New(http.StatusForbidden, "NOT_ENROLLED", "you are not enrolled in this subject")
	AlreadyMarked  = New(http.StatusOK, "ALREADY_MARKED", "attendance already recorded for this session")
)
