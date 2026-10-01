package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fictin/internal/models"
)

type SessionRepo struct{ db DBTX }

// ErrLiveSessionExists maps the one_live_session_per_subject partial unique index.
var ErrLiveSessionExists = errors.New("a live session already exists for this subject")

const sessionCols = `id, subject_id, schedule_id, status, started_at, ended_at, created_at`

func scanSession(row pgx.Row) (*models.ClassSession, error) {
	var s models.ClassSession
	err := row.Scan(&s.ID, &s.SubjectID, &s.ScheduleID, &s.Status, &s.StartedAt, &s.EndedAt, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

// StartLive inserts a new row directly in the 'live' state.
func (r *SessionRepo) StartLive(ctx context.Context, subjectID uuid.UUID, scheduleID *uuid.UUID) (*models.ClassSession, error) {
	const q = `INSERT INTO class_sessions (subject_id, schedule_id, status, started_at)
		VALUES ($1, $2, 'live', now())
		RETURNING ` + sessionCols
	s, err := scanSession(r.db.QueryRow(ctx, q, subjectID, scheduleID))
	if err != nil {
		if isUniqueViolation(err) && constraintName(err) == "one_live_session_per_subject" {
			return nil, ErrLiveSessionExists
		}
		return nil, err
	}
	return s, nil
}

func (r *SessionRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.ClassSession, error) {
	return scanSession(r.db.QueryRow(ctx, `SELECT `+sessionCols+` FROM class_sessions WHERE id = $1`, id))
}

// Stop transitions a live session to ended. Returns ErrNotFound if it was not live.
func (r *SessionRepo) Stop(ctx context.Context, id uuid.UUID) (*models.ClassSession, error) {
	const q = `UPDATE class_sessions SET status = 'ended', ended_at = now()
		WHERE id = $1 AND status = 'live'
		RETURNING ` + sessionCols
	return scanSession(r.db.QueryRow(ctx, q, id))
}

func (r *SessionRepo) ListLive(ctx context.Context) ([]models.ClassSession, error) {
	return r.list(ctx, `SELECT `+sessionCols+` FROM class_sessions WHERE status = 'live' ORDER BY started_at, id`)
}

func (r *SessionRepo) ListBySubject(ctx context.Context, subjectID uuid.UUID) ([]models.ClassSession, error) {
	return r.list(ctx, `SELECT `+sessionCols+` FROM class_sessions WHERE subject_id = $1 ORDER BY created_at DESC, id DESC`, subjectID)
}

func (r *SessionRepo) list(ctx context.Context, q string, args ...any) ([]models.ClassSession, error) {
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ClassSession{} // never nil: empty lists render as [] in JSON
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}
