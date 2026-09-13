// Package repository is the data-access layer: hand-written SQL over a pgx pool.
package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a lookup matches zero rows.
var ErrNotFound = errors.New("record not found")

// ErrConflict is returned on a unique-constraint violation.
var ErrConflict = errors.New("unique constraint violation")

// DBTX is satisfied by both *pgxpool.Pool and pgx.Tx.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store is the aggregate of all repositories plus access to the raw pool for
// transactions.
type Store struct {
	Pool       *pgxpool.Pool
	Users      *UserRepo
	Refresh    *RefreshTokenRepo
	Subjects   *SubjectRepo
	Schedules  *ScheduleRepo
	Enroll     *EnrollmentRepo
	Sessions   *SessionRepo
	QRTokens   *QRTokenRepo
	Attendance *AttendanceRepo
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		Pool:       pool,
		Users:      &UserRepo{db: pool},
		Refresh:    &RefreshTokenRepo{db: pool},
		Subjects:   &SubjectRepo{db: pool},
		Schedules:  &ScheduleRepo{db: pool},
		Enroll:     &EnrollmentRepo{db: pool},
		Sessions:   &SessionRepo{db: pool},
		QRTokens:   &QRTokenRepo{db: pool},
		Attendance: &AttendanceRepo{db: pool},
	}
}

// isUniqueViolation reports whether err is a Postgres 23505.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// constraintName returns the violated constraint/index name, or "".
func constraintName(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}
