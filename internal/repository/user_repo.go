package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fictin/internal/models"
)

type UserRepo struct{ db DBTX }

const userCols = `id, full_name, email, password_hash, role, student_code,
	is_active, must_change_password, created_at, updated_at`

func scanUser(row pgx.Row) (*models.User, error) {
	var u models.User
	err := row.Scan(&u.ID, &u.FullName, &u.Email, &u.PasswordHash, &u.Role,
		&u.StudentCode, &u.IsActive, &u.MustChangePassword, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

type CreateUserParams struct {
	FullName           string
	Email              string
	PasswordHash       string
	Role               models.Role
	StudentCode        *string
	MustChangePassword bool
}

func (r *UserRepo) Create(ctx context.Context, p CreateUserParams) (*models.User, error) {
	const q = `INSERT INTO users (full_name, email, password_hash, role, student_code, must_change_password)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING ` + userCols
	u, err := scanUser(r.db.QueryRow(ctx, q, p.FullName, p.Email, p.PasswordHash, p.Role, p.StudentCode, p.MustChangePassword))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: %s", ErrConflict, constraintName(err))
		}
		return nil, err
	}
	return u, nil
}

func (r *UserRepo) GetByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*models.User, error) {
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE lower(email) = lower($1)`, email))
}

func (r *UserRepo) GetByStudentCode(ctx context.Context, code string) (*models.User, error) {
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE student_code = $1`, code))
}

func (r *UserRepo) ExistsAdmin(ctx context.Context) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE role = 'admin')`).Scan(&exists)
	return exists, err
}

type ListUsersParams struct {
	Role   *models.Role
	Active *bool
	Limit  int
	Offset int
}

func (r *UserRepo) List(ctx context.Context, p ListUsersParams) ([]models.User, error) {
	var (
		conds []string
		args  []any
		i     = 1
	)
	if p.Role != nil {
		conds = append(conds, fmt.Sprintf("role = $%d", i))
		args = append(args, *p.Role)
		i++
	}
	if p.Active != nil {
		conds = append(conds, fmt.Sprintf("is_active = $%d", i))
		args = append(args, *p.Active)
		i++
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	if p.Limit <= 0 || p.Limit > 200 {
		p.Limit = 50
	}
	q := fmt.Sprintf(`SELECT %s FROM users %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`,
		userCols, where, i, i+1)
	args = append(args, p.Limit, p.Offset)

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

type UpdateUserParams struct {
	FullName    *string
	StudentCode *string
	IsActive    *bool
}

func (r *UserRepo) Update(ctx context.Context, id uuid.UUID, p UpdateUserParams) (*models.User, error) {
	var (
		sets []string
		args []any
		i    = 1
	)
	if p.FullName != nil {
		sets = append(sets, fmt.Sprintf("full_name = $%d", i))
		args = append(args, *p.FullName)
		i++
	}
	if p.StudentCode != nil {
		sets = append(sets, fmt.Sprintf("student_code = $%d", i))
		args = append(args, *p.StudentCode)
		i++
	}
	if p.IsActive != nil {
		sets = append(sets, fmt.Sprintf("is_active = $%d", i))
		args = append(args, *p.IsActive)
		i++
	}
	if len(sets) == 0 {
		return r.GetByID(ctx, id)
	}
	sets = append(sets, "updated_at = now()")
	q := fmt.Sprintf(`UPDATE users SET %s WHERE id = $%d RETURNING %s`,
		strings.Join(sets, ", "), i, userCols)
	args = append(args, id)

	u, err := scanUser(r.db.QueryRow(ctx, q, args...))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: %s", ErrConflict, constraintName(err))
		}
		return nil, err
	}
	return u, nil
}

func (r *UserRepo) SetPassword(ctx context.Context, id uuid.UUID, hash string, mustChange bool) error {
	ct, err := r.db.Exec(ctx,
		`UPDATE users SET password_hash = $1, must_change_password = $2, updated_at = now() WHERE id = $3`,
		hash, mustChange, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *UserRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.db.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
