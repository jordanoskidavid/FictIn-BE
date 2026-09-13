package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"fictin/internal/apperr"
	"fictin/internal/auth"
	"fictin/internal/httpresponse"
	"fictin/internal/service"
)

type AttendanceHandler struct {
	svc     *service.AttendanceService
	student *service.StudentService
}

func NewAttendanceHandler(svc *service.AttendanceService, student *service.StudentService) *AttendanceHandler {
	return &AttendanceHandler{svc: svc, student: student}
}

type scanRequest struct {
	SessionID string `json:"session_id" validate:"required,uuid"`
	Token     string `json:"token" validate:"required,min=8,max=128"`
}

func (h *AttendanceHandler) Scan(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	var req scanRequest
	if !decode(w, r, &req) {
		return
	}
	sessionID, perr := uuid.Parse(req.SessionID)
	if perr != nil {
		httpresponse.FromError(w, r, apperr.BadRequest.WithDetails("session_id must be a valid UUID"))
		return
	}
	res, err := h.svc.Scan(r.Context(), id.UserID, sessionID, req.Token)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, res)
}

func (h *AttendanceHandler) StudentHistory(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	rows, err := h.svc.ListForStudent(r.Context(), id.UserID)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, rows)
}

func (h *AttendanceHandler) StudentSubjects(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	subs, err := h.student.ListSubjects(r.Context(), id.UserID)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, subs)
}
