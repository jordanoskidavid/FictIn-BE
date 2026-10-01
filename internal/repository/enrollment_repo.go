package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"fictin/internal/models"
)

type EnrollmentRepo struct{ db DBTX }

// EnrolledStudent is an enrollment joined with the student's profile.
type EnrolledStudent struct {
	EnrollmentID uuid.UUID `json:"enrollment_id"`
	StudentID    uuid.UUID `json:"student_id"`
	FullName     string    `json:"full_name"`
	Email        string    `json:"email"`
	StudentCode  *string   `json:"student_code,omitempty"`
	EnrolledAt   string    `json:"enrolled_at"`
}

func (r *EnrollmentRepo) Create(ctx context.Context, subjectID, studentID uuid.UUID) (*models.Enrollment, error) {
	const q = `INSERT INTO enrollments (subject_id, student_id) VALUES ($1, $2)
		RETURNING id, subject_id, student_id, created_at`
	var e models.Enrollment
	err := r.db.QueryRow(ctx, q, subjectID, studentID).Scan(&e.ID, &e.SubjectID, &e.StudentID, &e.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: %s", ErrConflict, constraintName(err))
		}
		return nil, err
	}
	return &e, nil
}

func (r *EnrollmentRepo) Delete(ctx context.Context, subjectID, studentID uuid.UUID) error {
	ct, err := r.db.Exec(ctx, `DELETE FROM enrollments WHERE subject_id = $1 AND student_id = $2`, subjectID, studentID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *EnrollmentRepo) IsEnrolled(ctx context.Context, subjectID, studentID uuid.UUID) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM enrollments WHERE subject_id = $1 AND student_id = $2)`,
		subjectID, studentID).Scan(&ok)
	return ok, err
}

// IsEnrolledInSession checks enrollment against the subject that owns a session.
func (r *EnrollmentRepo) IsEnrolledInSession(ctx context.Context, sessionID, studentID uuid.UUID) (bool, error) {
	var ok bool
	const q = `SELECT EXISTS(
		SELECT 1 FROM enrollments e
		JOIN class_sessions cs ON cs.subject_id = e.subject_id
		WHERE cs.id = $1 AND e.student_id = $2)`
	err := r.db.QueryRow(ctx, q, sessionID, studentID).Scan(&ok)
	return ok, err
}

func (r *EnrollmentRepo) ListStudents(ctx context.Context, subjectID uuid.UUID) ([]EnrolledStudent, error) {
	const q = `SELECT e.id, u.id, u.full_name, u.email, u.student_code, e.created_at::text
		FROM enrollments e
		JOIN users u ON u.id = e.student_id
		WHERE e.subject_id = $1
		ORDER BY u.full_name`
	rows, err := r.db.Query(ctx, q, subjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EnrolledStudent{} // never nil: empty lists render as [] in JSON
	for rows.Next() {
		var s EnrolledStudent
		if err := rows.Scan(&s.EnrollmentID, &s.StudentID, &s.FullName, &s.Email, &s.StudentCode, &s.EnrolledAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
