package handlers

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"fictin/internal/apperr"
	"fictin/internal/auth"
	"fictin/internal/httpresponse"
	"fictin/internal/repository"
	"fictin/internal/service"
)

type SessionHandler struct {
	sessions   *service.SessionService
	attendance *service.AttendanceService
	qr         *service.QRManager
	store      *repository.Store
}

func NewSessionHandler(sessions *service.SessionService, attendance *service.AttendanceService, qr *service.QRManager, store *repository.Store) *SessionHandler {
	return &SessionHandler{sessions: sessions, attendance: attendance, qr: qr, store: store}
}

type startSessionRequest struct {
	ScheduleID *string `json:"schedule_id" validate:"omitempty,uuid"`
}

func (h *SessionHandler) Start(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	subjectID, err := uuidParam(r, "id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	var req startSessionRequest
	// Body is optional; tolerate an empty body.
	if r.ContentLength != 0 {
		if !decode(w, r, &req) {
			return
		}
	}
	var scheduleID *uuid.UUID
	if req.ScheduleID != nil && *req.ScheduleID != "" {
		parsed, perr := uuid.Parse(*req.ScheduleID)
		if perr != nil {
			httpresponse.FromError(w, r, apperr.BadRequest.WithDetails("schedule_id must be a valid UUID"))
			return
		}
		scheduleID = &parsed
	}
	sess, err := h.sessions.Start(r.Context(), subjectID, id.UserID, scheduleID)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.Created(w, sess)
}

func (h *SessionHandler) Stop(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	sessionID, err := uuidParam(r, "id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	sess, err := h.sessions.Stop(r.Context(), sessionID, id.UserID)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, sess)
}

func (h *SessionHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	sessionID, err := uuidParam(r, "id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	view, err := h.sessions.GetForCaller(r.Context(), sessionID, id.UserID, id.IsAdmin())
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, view)
}

// CurrentQR is the polling fallback for the professor board (spec 7.2).
func (h *SessionHandler) CurrentQR(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	sessionID, err := uuidParam(r, "id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	sess, err := h.sessions.LoadOwnedByProfessor(r.Context(), sessionID, id.UserID)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	if sess.Status != "live" {
		httpresponse.FromError(w, r, apperr.SessionNotLive)
		return
	}

	if msg, ok := h.qr.Current(sessionID); ok {
		httpresponse.OK(w, msg)
		return
	}
	// Fall back to the DB (e.g. right after a restart, before the first tick).
	tok, err := h.store.QRTokens.Latest(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			httpresponse.FromError(w, r, apperr.NotFound.WithDetails("no token issued yet"))
			return
		}
		httpresponse.FromError(w, r, apperr.Internal.WithCause(err))
		return
	}
	httpresponse.OK(w, service.TokenMessage{
		SessionID: tok.SessionID.String(),
		Token:     tok.Token,
		IssuedAt:  tok.IssuedAt,
		ExpiresAt: tok.ExpiresAt,
	})
}

func (h *SessionHandler) SessionAttendance(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	sessionID, err := uuidParam(r, "id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	rows, err := h.attendance.ListForSession(r.Context(), sessionID, id.UserID)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, rows)
}
