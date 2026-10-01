package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"fictin/internal/apperr"
	"fictin/internal/auth"
	"fictin/internal/httpresponse"
	"fictin/internal/service"
)

type SubjectHandler struct {
	svc *service.SubjectService
}

func NewSubjectHandler(svc *service.SubjectService) *SubjectHandler {
	return &SubjectHandler{svc: svc}
}

type createSubjectRequest struct {
	Name string `json:"name" validate:"required,min=2,max=150"`
	Code string `json:"code" validate:"required,min=2,max=30"`
}

func (h *SubjectHandler) Create(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	var req createSubjectRequest
	if !decode(w, r, &req) {
		return
	}
	subj, err := h.svc.Create(r.Context(), id.UserID, req.Name, req.Code)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.Created(w, subj)
}

func (h *SubjectHandler) List(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	subs, err := h.svc.ListOwned(r.Context(), id.UserID)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, subs)
}

func (h *SubjectHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	subjectID, err := uuidParam(r, "id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	detail, err := h.svc.Detail(r.Context(), subjectID, id.UserID)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, detail)
}

type updateSubjectRequest struct {
	Name *string `json:"name" validate:"omitempty,min=2,max=150"`
	Code *string `json:"code" validate:"omitempty,min=2,max=30"`
}

func (h *SubjectHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	subjectID, err := uuidParam(r, "id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	var req updateSubjectRequest
	if !decode(w, r, &req) {
		return
	}
	subj, err := h.svc.Update(r.Context(), subjectID, id.UserID, req.Name, req.Code)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, subj)
}

func (h *SubjectHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	subjectID, err := uuidParam(r, "id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	if err := h.svc.Delete(r.Context(), subjectID, id.UserID); err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.NoContent(w)
}

type createScheduleRequest struct {
	DayOfWeek int16   `json:"day_of_week" validate:"min=0,max=6"`
	StartTime string  `json:"start_time" validate:"required"`
	EndTime   string  `json:"end_time" validate:"required"`
	Room      *string `json:"room" validate:"omitempty,max=100"`
}

func (h *SubjectHandler) AddSchedule(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	subjectID, err := uuidParam(r, "id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	var req createScheduleRequest
	if !decode(w, r, &req) {
		return
	}
	sched, err := h.svc.AddSchedule(r.Context(), subjectID, id.UserID, service.CreateScheduleInput{
		DayOfWeek: req.DayOfWeek,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
		Room:      req.Room,
	})
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.Created(w, sched)
}

func (h *SubjectHandler) ListSchedules(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	subjectID, err := uuidParam(r, "id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	out, err := h.svc.ListSchedules(r.Context(), subjectID, id.UserID)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, out)
}

func (h *SubjectHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	subjectID, err := uuidParam(r, "id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	out, err := h.svc.ListSessions(r.Context(), subjectID, id.UserID)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, out)
}

type enrollRequest struct {
	StudentID   *string `json:"student_id" validate:"omitempty,uuid"`
	StudentCode *string `json:"student_code" validate:"omitempty,max=50"`
}

func (h *SubjectHandler) Enroll(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	subjectID, err := uuidParam(r, "id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	var req enrollRequest
	if !decode(w, r, &req) {
		return
	}
	var studentID *uuid.UUID
	if req.StudentID != nil && *req.StudentID != "" {
		parsed, perr := uuid.Parse(*req.StudentID)
		if perr != nil {
			httpresponse.FromError(w, r, apperr.BadRequest.WithDetails("student_id must be a valid UUID"))
			return
		}
		studentID = &parsed
	}
	enr, err := h.svc.Enroll(r.Context(), subjectID, id.UserID, studentID, req.StudentCode)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.Created(w, enr)
}

func (h *SubjectHandler) ListRoster(w http.ResponseWriter, r *http.Request) {
	id := auth.MustIdentity(r.Context())
	subjectID, err := uuidParam(r, "id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	out, err := h.svc.ListRoster(r.Context(), subjectID, id.UserID)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, out)
}
