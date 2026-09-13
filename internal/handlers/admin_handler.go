package handlers

import (
	"encoding/csv"
	"net/http"

	"fictin/internal/httpresponse"
	"fictin/internal/models"
	"fictin/internal/repository"
	"fictin/internal/service"
)

type AdminHandler struct {
	svc *service.AdminService
}

func NewAdminHandler(svc *service.AdminService) *AdminHandler {
	return &AdminHandler{svc: svc}
}

func (h *AdminHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	var rolePtr *models.Role
	if raw := r.URL.Query().Get("role"); raw != "" {
		role := models.Role(raw)
		rolePtr = &role
	}
	var activePtr *bool
	switch r.URL.Query().Get("active") {
	case "true":
		v := true
		activePtr = &v
	case "false":
		v := false
		activePtr = &v
	}
	users, err := h.svc.ListUsers(r.Context(), rolePtr, activePtr,
		queryInt(r, "limit", 50), queryInt(r, "offset", 0))
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, users)
}

type updateUserRequest struct {
	FullName    *string `json:"full_name" validate:"omitempty,min=2,max=150"`
	StudentCode *string `json:"student_code" validate:"omitempty,max=50"`
	IsActive    *bool   `json:"is_active"`
}

func (h *AdminHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	userID, err := uuidParam(r, "id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	var req updateUserRequest
	if !decode(w, r, &req) {
		return
	}
	u, err := h.svc.UpdateUser(r.Context(), userID, service.UpdateUserInput{
		FullName: req.FullName, StudentCode: req.StudentCode, IsActive: req.IsActive,
	})
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, u)
}

func (h *AdminHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	userID, err := uuidParam(r, "id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	if err := h.svc.DeleteUser(r.Context(), userID); err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.NoContent(w)
}

func (h *AdminHandler) ListSubjects(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.ListSubjects(r.Context())
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	httpresponse.OK(w, out)
}

func (h *AdminHandler) ListAttendance(w http.ResponseWriter, r *http.Request) {
	sessionID, err := optionalUUIDQuery(r, "session_id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	subjectID, err := optionalUUIDQuery(r, "subject_id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	studentID, err := optionalUUIDQuery(r, "student_id")
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}
	f := repository.ListFilter{
		SessionID: sessionID,
		SubjectID: subjectID,
		StudentID: studentID,
		Limit:     queryInt(r, "limit", 500),
		Offset:    queryInt(r, "offset", 0),
	}
	rows, err := h.svc.ListAttendance(r.Context(), f)
	if err != nil {
		httpresponse.FromError(w, r, err)
		return
	}

	if r.URL.Query().Get("format") == "csv" {
		writeAttendanceCSV(w, rows)
		return
	}
	httpresponse.OK(w, rows)
}

func writeAttendanceCSV(w http.ResponseWriter, rows []repository.AttendanceRow) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="attendance.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"record_id", "session_id", "subject_code", "subject_name", "student_code", "student_name", "scanned_at"})
	for _, row := range rows {
		code := ""
		if row.StudentCode != nil {
			code = *row.StudentCode
		}
		_ = cw.Write([]string{
			row.ID.String(), row.SessionID.String(), row.SubjectCode, row.SubjectName,
			code, row.StudentName, row.ScannedAt,
		})
	}
	cw.Flush()
}
