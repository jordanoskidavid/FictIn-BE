package service

import (
	"context"

	"github.com/google/uuid"

	"fictin/internal/apperr"
	"fictin/internal/models"
	"fictin/internal/repository"
)

type StudentService struct {
	store *repository.Store
}

func NewStudentService(store *repository.Store) *StudentService {
	return &StudentService{store: store}
}

func (s *StudentService) ListSubjects(ctx context.Context, studentID uuid.UUID) ([]models.Subject, error) {
	out, err := s.store.Subjects.ListForStudent(ctx, studentID)
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}
	return out, nil
}
