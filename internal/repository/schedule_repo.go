package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fictin/internal/models"
)

type ScheduleRepo struct{ db DBTX }

const scheduleCols = `id, subject_id, day_of_week, start_time::text, end_time::text, room, created_at`

func scanSchedule(row pgx.Row) (*models.ClassSchedule, error) {
	var s models.ClassSchedule
	err := row.Scan(&s.ID, &s.SubjectID, &s.DayOfWeek, &s.StartTime, &s.EndTime, &s.Room, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

type CreateScheduleParams struct {
	SubjectID uuid.UUID
	DayOfWeek int16
	StartTime string
	EndTime   string
	Room      *string
}

func (r *ScheduleRepo) Create(ctx context.Context, p CreateScheduleParams) (*models.ClassSchedule, error) {
	const q = `INSERT INTO class_schedules (subject_id, day_of_week, start_time, end_time, room)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING ` + scheduleCols
	return scanSchedule(r.db.QueryRow(ctx, q, p.SubjectID, p.DayOfWeek, p.StartTime, p.EndTime, p.Room))
}

func (r *ScheduleRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.ClassSchedule, error) {
	return scanSchedule(r.db.QueryRow(ctx, `SELECT `+scheduleCols+` FROM class_schedules WHERE id = $1`, id))
}

func (r *ScheduleRepo) ListBySubject(ctx context.Context, subjectID uuid.UUID) ([]models.ClassSchedule, error) {
	const q = `SELECT ` + scheduleCols + ` FROM class_schedules WHERE subject_id = $1
		ORDER BY day_of_week, start_time`
	rows, err := r.db.Query(ctx, q, subjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.ClassSchedule
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (r *ScheduleRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.db.Exec(ctx, `DELETE FROM class_schedules WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
