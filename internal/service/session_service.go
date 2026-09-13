package service

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"fictin/internal/apperr"
	"fictin/internal/models"
	"fictin/internal/repository"
)

type SessionService struct {
	store    *repository.Store
	subjects *SubjectService
	qr       *QRManager
}

func NewSessionService(store *repository.Store, subjects *SubjectService, qr *QRManager) *SessionService {
	return &SessionService{store: store, subjects: subjects, qr: qr}
}

// Start creates a live session for a subject owned by the professor and kicks
// off QR rotation.
func (s *SessionService) Start(ctx context.Context, subjectID, professorID uuid.UUID, scheduleID *uuid.UUID) (*models.ClassSession, error) {
	if _, err := s.subjects.RequireOwnership(ctx, subjectID, professorID); err != nil {
		return nil, err
	}

	if scheduleID != nil {
		sched, err := s.store.Schedules.GetByID(ctx, *scheduleID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return nil, apperr.Validation.WithDetails("schedule_id not found")
			}
			return nil, apperr.Internal.WithCause(err)
		}
		if sched.SubjectID != subjectID {
			return nil, apperr.Validation.WithDetails("schedule_id does not belong to this subject")
		}
	}

	sess, err := s.store.Sessions.StartLive(ctx, subjectID, scheduleID)
	if err != nil {
		if errors.Is(err, repository.ErrLiveSessionExists) {
			return nil, apperr.Conflict.WithDetails("a live session already exists for this subject")
		}
		return nil, apperr.Internal.WithCause(err)
	}

	s.qr.Start(sess.ID)
	return sess, nil
}

// Stop ends a live session and cancels its QR rotation.
func (s *SessionService) Stop(ctx context.Context, sessionID, professorID uuid.UUID) (*models.ClassSession, error) {
	sess, err := s.loadOwned(ctx, sessionID, professorID)
	if err != nil {
		return nil, err
	}
	if sess.Status != models.SessionLive {
		return nil, apperr.Conflict.WithDetails("session is not live")
	}

	ended, err := s.store.Sessions.Stop(ctx, sessionID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// Lost a race; treat as already stopped.
			s.qr.Stop(sessionID)
			return nil, apperr.Conflict.WithDetails("session is not live")
		}
		return nil, apperr.Internal.WithCause(err)
	}
	s.qr.Stop(sessionID)
	return ended, nil
}

// SessionView adds live rotation status to a session.
type SessionView struct {
	*models.ClassSession
	QRRotating bool `json:"qr_rotating"`
}

func (s *SessionService) loadOwned(ctx context.Context, sessionID, professorID uuid.UUID) (*models.ClassSession, error) {
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
	return sess, nil
}

// LoadOwnedByProfessor is exported for the QR/attendance handlers that need an
// ownership check on a session.
func (s *SessionService) LoadOwnedByProfessor(ctx context.Context, sessionID, professorID uuid.UUID) (*models.ClassSession, error) {
	return s.loadOwned(ctx, sessionID, professorID)
}

// GetForCaller returns a session, allowing the owning professor or any admin.
func (s *SessionService) GetForCaller(ctx context.Context, sessionID, callerID uuid.UUID, isAdmin bool) (*SessionView, error) {
	sess, err := s.store.Sessions.GetByID(ctx, sessionID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.NotFound.WithDetails("session not found")
		}
		return nil, apperr.Internal.WithCause(err)
	}
	if !isAdmin {
		subj, err := s.store.Subjects.GetByID(ctx, sess.SubjectID)
		if err != nil {
			return nil, apperr.Internal.WithCause(err)
		}
		if subj.ProfessorID != callerID {
			return nil, apperr.Forbidden.WithDetails("you do not own this session")
		}
	}
	return &SessionView{ClassSession: sess, QRRotating: s.qr.IsRunning(sess.ID)}, nil
}
