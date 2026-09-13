package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fictin/internal/models"
)

type SubjectRepo struct{ db DBTX }

const subjectCols = `id, name, code, professor_id, created_at, updated_at`

func scanSubject(row pgx.Row) (*models.Subject, error) {
	var s models.Subject
	err := row.Scan(&s.ID, &s.Name, &s.Code, &s.ProfessorID, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

func (r *SubjectRepo) Create(ctx context.Context, name, code string, professorID uuid.UUID) (*models.Subject, error) {
	const q = `INSERT INTO subjects (name, code, professor_id) VALUES ($1, $2, $3) RETURNING ` + subjectCols
	s, err := scanSubject(r.db.QueryRow(ctx, q, name, code, professorID))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: %s", ErrConflict, constraintName(err))
		}
		return nil, err
	}
	return s, nil
}

func (r *SubjectRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.Subject, error) {
	return scanSubject(r.db.QueryRow(ctx, `SELECT `+subjectCols+` FROM subjects WHERE id = $1`, id))
}

func (r *SubjectRepo) ListByProfessor(ctx context.Context, professorID uuid.UUID) ([]models.Subject, error) {
	return r.list(ctx, `SELECT `+subjectCols+` FROM subjects WHERE professor_id = $1 ORDER BY created_at DESC`, professorID)
}

func (r *SubjectRepo) ListAll(ctx context.Context) ([]models.Subject, error) {
	return r.list(ctx, `SELECT `+subjectCols+` FROM subjects ORDER BY created_at DESC`)
}

func (r *SubjectRepo) list(ctx context.Context, q string, args ...any) ([]models.Subject, error) {
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Subject
	for rows.Next() {
		s, err := scanSubject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

type UpdateSubjectParams struct {
	Name *string
	Code *string
}

func (r *SubjectRepo) Update(ctx context.Context, id uuid.UUID, p UpdateSubjectParams) (*models.Subject, error) {
	const q = `UPDATE subjects SET
		name = COALESCE($2, name),
		code = COALESCE($3, code),
		updated_at = now()
		WHERE id = $1
		RETURNING ` + subjectCols
	s, err := scanSubject(r.db.QueryRow(ctx, q, id, p.Name, p.Code))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: %s", ErrConflict, constraintName(err))
		}
		return nil, err
	}
	return s, nil
}

func (r *SubjectRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.db.Exec(ctx, `DELETE FROM subjects WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListForStudent returns subjects the student is enrolled in.
func (r *SubjectRepo) ListForStudent(ctx context.Context, studentID uuid.UUID) ([]models.Subject, error) {
	const q = `SELECT s.id, s.name, s.code, s.professor_id, s.created_at, s.updated_at
		FROM subjects s
		JOIN enrollments e ON e.subject_id = s.id
		WHERE e.student_id = $1
		ORDER BY s.name`
	return r.list(ctx, q, studentID)
}
