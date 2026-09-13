package repository

import (
	"context"
	"strconv"

	"github.com/google/uuid"

	"fictin/internal/models"
)

type AttendanceRepo struct{ db DBTX }

// AttendanceRow is an attendance record joined with student + session context.
type AttendanceRow struct {
	ID          uuid.UUID `json:"id"`
	SessionID   uuid.UUID `json:"session_id"`
	SubjectID   uuid.UUID `json:"subject_id"`
	SubjectName string    `json:"subject_name"`
	SubjectCode string    `json:"subject_code"`
	StudentID   uuid.UUID `json:"student_id"`
	StudentName string    `json:"student_name"`
	StudentCode *string   `json:"student_code,omitempty"`
	ScannedAt   string    `json:"scanned_at"`
}

func (r *AttendanceRepo) Exists(ctx context.Context, sessionID, studentID uuid.UUID) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM attendance_records WHERE session_id = $1 AND student_id = $2)`,
		sessionID, studentID).Scan(&ok)
	return ok, err
}

func (r *AttendanceRepo) Insert(ctx context.Context, sessionID, studentID uuid.UUID, qrTokenID *uuid.UUID) (*models.AttendanceRecord, error) {
	const q = `INSERT INTO attendance_records (session_id, student_id, qr_token_id)
		VALUES ($1, $2, $3)
		RETURNING id, session_id, student_id, qr_token_id, scanned_at`
	var a models.AttendanceRecord
	err := r.db.QueryRow(ctx, q, sessionID, studentID, qrTokenID).
		Scan(&a.ID, &a.SessionID, &a.StudentID, &a.QRTokenID, &a.ScannedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrConflict
		}
		return nil, err
	}
	return &a, nil
}

// ListFilter drives the admin export and the professor/student views.
type ListFilter struct {
	SessionID *uuid.UUID
	SubjectID *uuid.UUID
	StudentID *uuid.UUID
	Limit     int
	Offset    int
}

func (r *AttendanceRepo) List(ctx context.Context, f ListFilter) ([]AttendanceRow, error) {
	q := `SELECT ar.id, ar.session_id, s.id, s.name, s.code,
			u.id, u.full_name, u.student_code, ar.scanned_at::text
		FROM attendance_records ar
		JOIN class_sessions cs ON cs.id = ar.session_id
		JOIN subjects s ON s.id = cs.subject_id
		JOIN users u ON u.id = ar.student_id
		WHERE 1=1`
	var args []any
	i := 1
	if f.SessionID != nil {
		q += " AND ar.session_id = $" + itoa(i)
		args = append(args, *f.SessionID)
		i++
	}
	if f.SubjectID != nil {
		q += " AND cs.subject_id = $" + itoa(i)
		args = append(args, *f.SubjectID)
		i++
	}
	if f.StudentID != nil {
		q += " AND ar.student_id = $" + itoa(i)
		args = append(args, *f.StudentID)
		i++
	}
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 200
	}
	q += " ORDER BY ar.scanned_at DESC LIMIT $" + itoa(i) + " OFFSET $" + itoa(i+1)
	args = append(args, f.Limit, f.Offset)

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttendanceRow
	for rows.Next() {
		var a AttendanceRow
		if err := rows.Scan(&a.ID, &a.SessionID, &a.SubjectID, &a.SubjectName, &a.SubjectCode,
			&a.StudentID, &a.StudentName, &a.StudentCode, &a.ScannedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func itoa(i int) string { return strconv.Itoa(i) }
