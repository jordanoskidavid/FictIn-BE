// Package httpresponse provides a single JSON envelope for all API responses.
//
// Success:  {"data": <payload>}
// Error:    {"error": {"code": "SNAKE_CASE_CODE", "message": "human readable", "details": {...}}}
package httpresponse

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type envelope struct {
	Data  any    `json:"data,omitempty"`
	Error *Error `json:"error,omitempty"`
}

// Error is the machine-readable error body.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// JSON writes an arbitrary payload wrapped in {"data": ...}.
func JSON(w http.ResponseWriter, status int, data any) {
	write(w, status, envelope{Data: data})
}

// Created is a 201 helper.
func Created(w http.ResponseWriter, data any) {
	JSON(w, http.StatusCreated, data)
}

// OK is a 200 helper.
func OK(w http.ResponseWriter, data any) {
	JSON(w, http.StatusOK, data)
}

// NoContent writes a bare 204.
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// Fail writes an error envelope with the given status, code and message.
func Fail(w http.ResponseWriter, status int, code, message string) {
	write(w, status, envelope{Error: &Error{Code: code, Message: message}})
}

// FailWithDetails is Fail plus a structured details payload (e.g. validation errors).
func FailWithDetails(w http.ResponseWriter, status int, code, message string, details any) {
	write(w, status, envelope{Error: &Error{Code: code, Message: message, Details: details}})
}

func write(w http.ResponseWriter, status int, body envelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if status == http.StatusNoContent {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("failed to encode response", "err", err)
	}
}
