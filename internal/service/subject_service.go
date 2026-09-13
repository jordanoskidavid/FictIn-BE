package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"fictin/internal/apperr"
	"fictin/internal/models"
	"fictin/internal/repository"
)

type SubjectService struct {
	store *repository.Store
}

func NewSubjectService(store *repository.Store) *SubjectService {
	return &SubjectService{store: store}
}

// RequireOwnership loads the subject and verifies the professor owns it.
// Admins are allowed through for read paths that pass allowAdmin=true.
func (s *SubjectService) RequireOwnership(ctx context.Context, subjectID, professorID uuid.UUID) (*models.Subject, error) {
	subj, err := s.store.Subjects.GetByID(ctx, subjectID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.NotFound.WithDetails("subject not found")
		}
		return nil, apperr.Internal.WithCause(err)
	}
	if subj.ProfessorID != professorID {
		return nil, apperr.Forbidden.WithDetails("you do not own this subject")
	}
	return subj, nil
}

func (s *SubjectService) Create(ctx context.Context, professorID uuid.UUID, name, code string) (*models.Subject, error) {
	name = strings.TrimSpace(name)
	code = strings.ToUpper(strings.TrimSpace(code))
	subj, err := s.store.Subjects.Create(ctx, name, code, professorID)
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return nil, apperr.Conflict.WithDetails("subject code already in use")
		}
		return nil, apperr.Internal.WithCause(err)
	}
	return subj, nil
}

func (s *SubjectService) ListOwned(ctx context.Context, professorID uuid.UUID) ([]models.Subject, error) {
	out, err := s.store.Subjects.ListByProfessor(ctx, professorID)
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}
	return out, nil
}

// SubjectDetail is a subject plus its roster and schedules.
type SubjectDetail struct {
	Subject   *models.Subject              `json:"subject"`
	Schedules []models.ClassSchedule       `json:"schedules"`
	Roster    []repository.EnrolledStudent `json:"roster"`
}

func (s *SubjectService) Detail(ctx context.Context, subjectID, professorID uuid.UUID) (*SubjectDetail, error) {
	subj, err := s.RequireOwnership(ctx, subjectID, professorID)
	if err != nil {
		return nil, err
	}
	sched, err := s.store.Schedules.ListBySubject(ctx, subjectID)
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}
	roster, err := s.store.Enroll.ListStudents(ctx, subjectID)
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}
	return &SubjectDetail{Subject: subj, Schedules: sched, Roster: roster}, nil
}

func (s *SubjectService) Update(ctx context.Context, subjectID, professorID uuid.UUID, name, code *string) (*models.Subject, error) {
	if _, err := s.RequireOwnership(ctx, subjectID, professorID); err != nil {
		return nil, err
	}
	if code != nil {
		up := strings.ToUpper(strings.TrimSpace(*code))
		code = &up
	}
	if name != nil {
		tn := strings.TrimSpace(*name)
		name = &tn
	}
	subj, err := s.store.Subjects.Update(ctx, subjectID, repository.UpdateSubjectParams{Name: name, Code: code})
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return nil, apperr.Conflict.WithDetails("subject code already in use")
		}
		return nil, apperr.Internal.WithCause(err)
	}
	return subj, nil
}

func (s *SubjectService) Delete(ctx context.Context, subjectID, professorID uuid.UUID) error {
	if _, err := s.RequireOwnership(ctx, subjectID, professorID); err != nil {
		return err
	}
	if err := s.store.Subjects.Delete(ctx, subjectID); err != nil {
		return apperr.Internal.WithCause(err)
	}
	return nil
}

// ---- schedules ----

type CreateScheduleInput struct {
	DayOfWeek int16
	StartTime string
	EndTime   string
	Room      *string
}

func (s *SubjectService) AddSchedule(ctx context.Context, subjectID, professorID uuid.UUID, in CreateScheduleInput) (*models.ClassSchedule, error) {
	if _, err := s.RequireOwnership(ctx, subjectID, professorID); err != nil {
		return nil, err
	}
	if in.DayOfWeek < 0 || in.DayOfWeek > 6 {
		return nil, apperr.Validation.WithDetails("day_of_week must be 0..6")
	}
	if !validTime(in.StartTime) || !validTime(in.EndTime) {
		return nil, apperr.Validation.WithDetails("start_time and end_time must be HH:MM or HH:MM:SS")
	}
	if in.EndTime <= in.StartTime {
		return nil, apperr.Validation.WithDetails("end_time must be after start_time")
	}
	sched, err := s.store.Schedules.Create(ctx, repository.CreateScheduleParams{
		SubjectID: subjectID,
		DayOfWeek: in.DayOfWeek,
		StartTime: in.StartTime,
		EndTime:   in.EndTime,
		Room:      in.Room,
	})
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}
	return sched, nil
}

func (s *SubjectService) ListSchedules(ctx context.Context, subjectID, professorID uuid.UUID) ([]models.ClassSchedule, error) {
	if _, err := s.RequireOwnership(ctx, subjectID, professorID); err != nil {
		return nil, err
	}
	out, err := s.store.Schedules.ListBySubject(ctx, subjectID)
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}
	return out, nil
}

// ---- enrollments ----

// Enroll adds a student (by id or student_code) to a subject.
func (s *SubjectService) Enroll(ctx context.Context, subjectID, professorID uuid.UUID, studentID *uuid.UUID, studentCode *string) (*models.Enrollment, error) {
	if _, err := s.RequireOwnership(ctx, subjectID, professorID); err != nil {
		return nil, err
	}

	var student *models.User
	var err error
	switch {
	case studentID != nil:
		student, err = s.store.Users.GetByID(ctx, *studentID)
	case studentCode != nil && strings.TrimSpace(*studentCode) != "":
		student, err = s.store.Users.GetByStudentCode(ctx, strings.TrimSpace(*studentCode))
	default:
		return nil, apperr.Validation.WithDetails("provide student_id or student_code")
	}
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.NotFound.WithDetails("student not found")
		}
		return nil, apperr.Internal.WithCause(err)
	}
	if student.Role != models.RoleStudent {
		return nil, apperr.Validation.WithDetails("target user is not a student")
	}
	if !student.IsActive {
		return nil, apperr.Validation.WithDetails("student account is inactive")
	}

	enr, err := s.store.Enroll.Create(ctx, subjectID, student.ID)
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return nil, apperr.Conflict.WithDetails("student already enrolled")
		}
		return nil, apperr.Internal.WithCause(err)
	}
	return enr, nil
}

func (s *SubjectService) ListRoster(ctx context.Context, subjectID, professorID uuid.UUID) ([]repository.EnrolledStudent, error) {
	if _, err := s.RequireOwnership(ctx, subjectID, professorID); err != nil {
		return nil, err
	}
	out, err := s.store.Enroll.ListStudents(ctx, subjectID)
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}
	return out, nil
}

func validTime(s string) bool {
	if _, err := time.Parse("15:04", s); err == nil {
		return true
	}
	_, err := time.Parse("15:04:05", s)
	return err == nil
}
