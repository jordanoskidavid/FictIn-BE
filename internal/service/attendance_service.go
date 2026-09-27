package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"fictin/internal/apperr"
	"fictin/internal/clock"
	"fictin/internal/models"
	"fictin/internal/repository"
)

type AttendanceService struct {
	store *repository.Store
	clock clock.Clock
	log   *slog.Logger
}

func NewAttendanceService(store *repository.Store, clk clock.Clock, log *slog.Logger) *AttendanceService {
	return &AttendanceService{store: store, clock: clk, log: log}
}

// checkScanToken decides whether a stored token may mark attendance for
// sessionID at time now. A token from another session is reported as invalid,
// not expired, so it reveals nothing about other sessions.
func checkScanToken(tok *models.QRToken, sessionID uuid.UUID, now time.Time) error {
	if tok == nil || tok.SessionID != sessionID {
		return apperr.TokenInvalid
	}
	if now.After(tok.ExpiresAt) {
		return apperr.TokenExpired
	}
	return nil
}

// ScanResult reports the outcome of a scan.
type ScanResult struct {
	Status    string                   `json:"status"` // "marked" | "already_marked"
	Record    *models.AttendanceRecord `json:"record,omitempty"`
	ScannedAt string                   `json:"scanned_at,omitempty"`
}

// Scan validates a QR token and records attendance (spec §7.3).
func (s *AttendanceService) Scan(ctx context.Context, studentID, sessionID uuid.UUID, token string) (*ScanResult, error) {
	audit := func(outcome string) {
		s.log.InfoContext(ctx, "attendance scan",
			"student_id", studentID, "session_id", sessionID,
			"token_prefix", tokenPrefix(token), "outcome", outcome)
	}

	sess, err := s.store.Sessions.GetByID(ctx, sessionID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			audit("session_not_found")
			return nil, apperr.NotFound.WithDetails("session not found")
		}
		return nil, apperr.Internal.WithCause(err)
	}
	if sess.Status != models.SessionLive {
		audit("session_not_live")
		return nil, apperr.SessionNotLive
	}

	tok, err := s.store.QRTokens.GetByToken(ctx, token)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		audit("token_invalid")
		return nil, apperr.TokenInvalid
	case err != nil:
		return nil, apperr.Internal.WithCause(err)
	}
	if err := checkScanToken(tok, sessionID, s.clock.Now()); err != nil {
		if errors.Is(err, apperr.TokenExpired) {
			audit("token_expired")
		} else {
			audit("token_invalid")
		}
		return nil, err
	}

	enrolled, err := s.store.Enroll.IsEnrolledInSession(ctx, sessionID, studentID)
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}
	if !enrolled {
		audit("not_enrolled")
		return nil, apperr.NotEnrolled
	}

	exists, err := s.store.Attendance.Exists(ctx, sessionID, studentID)
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}
	if exists {
		audit("already_marked")
		return &ScanResult{Status: "already_marked"}, nil
	}

	tokenID := tok.ID
	rec, err := s.store.Attendance.Insert(ctx, sessionID, studentID, &tokenID)
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			audit("already_marked")
			return &ScanResult{Status: "already_marked"}, nil
		}
		return nil, apperr.Internal.WithCause(err)
	}

	audit("marked")
	return &ScanResult{Status: "marked", Record: rec, ScannedAt: repository.FormatTimestamp(rec.ScannedAt)}, nil
}

func (s *AttendanceService) ListForSession(ctx context.Context, sessionID, professorID uuid.UUID) ([]repository.AttendanceRow, error) {
	sess, err := s.store.Sessions.GetByID(ctx, sessionID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.NotFound.WithDetails("session not found")
		}
		return nil, apperr.Internal.WithCause(err)
	}
	subj, err := s.store.Subjects.GetByID(ctx, sess.SubjectID)
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}
	if subj.ProfessorID != professorID {
		return nil, apperr.Forbidden.WithDetails("you do not own this session")
	}
	rows, err := s.store.Attendance.List(ctx, repository.ListFilter{SessionID: &sessionID})
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}
	return rows, nil
}

func (s *AttendanceService) ListForStudent(ctx context.Context, studentID uuid.UUID) ([]repository.AttendanceRow, error) {
	rows, err := s.store.Attendance.List(ctx, repository.ListFilter{StudentID: &studentID})
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}
	return rows, nil
}

func tokenPrefix(t string) string {
	if len(t) <= 6 {
		return "******"
	}
	return t[:6] + "…"
}
