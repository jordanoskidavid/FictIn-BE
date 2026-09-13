package service

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"fictin/internal/apperr"
	"fictin/internal/models"
	"fictin/internal/repository"
)

type AdminService struct {
	store *repository.Store
}

func NewAdminService(store *repository.Store) *AdminService {
	return &AdminService{store: store}
}

func (s *AdminService) ListUsers(ctx context.Context, role *models.Role, active *bool, limit, offset int) ([]models.User, error) {
	if role != nil && !role.Valid() {
		return nil, apperr.Validation.WithDetails("invalid role filter")
	}
	out, err := s.store.Users.List(ctx, repository.ListUsersParams{
		Role: role, Active: active, Limit: limit, Offset: offset,
	})
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}
	return out, nil
}

type UpdateUserInput struct {
	FullName    *string
	StudentCode *string
	IsActive    *bool
}

func (s *AdminService) UpdateUser(ctx context.Context, id uuid.UUID, in UpdateUserInput) (*models.User, error) {
	if in.FullName != nil {
		t := strings.TrimSpace(*in.FullName)
		if t == "" {
			return nil, apperr.Validation.WithDetails("full_name cannot be empty")
		}
		in.FullName = &t
	}
	u, err := s.store.Users.Update(ctx, id, repository.UpdateUserParams{
		FullName: in.FullName, StudentCode: in.StudentCode, IsActive: in.IsActive,
	})
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.NotFound.WithDetails("user not found")
		}
		if errors.Is(err, repository.ErrConflict) {
			return nil, apperr.Conflict.WithDetails("student_code already in use")
		}
		return nil, apperr.Internal.WithCause(err)
	}
	// Deactivation kills all sessions.
	if in.IsActive != nil && !*in.IsActive {
		_ = s.store.Refresh.RevokeAllForUser(ctx, id)
	}
	return u, nil
}

func (s *AdminService) DeleteUser(ctx context.Context, id uuid.UUID) error {
	if err := s.store.Users.Delete(ctx, id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apperr.NotFound.WithDetails("user not found")
		}
		return apperr.Internal.WithCause(err)
	}
	return nil
}

func (s *AdminService) ListSubjects(ctx context.Context) ([]models.Subject, error) {
	out, err := s.store.Subjects.ListAll(ctx)
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}
	return out, nil
}

func (s *AdminService) ListAttendance(ctx context.Context, f repository.ListFilter) ([]repository.AttendanceRow, error) {
	out, err := s.store.Attendance.List(ctx, f)
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}
	return out, nil
}
