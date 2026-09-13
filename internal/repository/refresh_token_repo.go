package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fictin/internal/models"
)

type RefreshTokenRepo struct{ db DBTX }

func (r *RefreshTokenRepo) Create(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		userID, tokenHash, expiresAt)
	return err
}

// GetActiveByHash returns a non-revoked, non-expired token row.
func (r *RefreshTokenRepo) GetActiveByHash(ctx context.Context, tokenHash string) (*models.RefreshToken, error) {
	const q = `SELECT id, user_id, token_hash, expires_at, revoked_at, created_at
		FROM refresh_tokens
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()`
	var t models.RefreshToken
	err := r.db.QueryRow(ctx, q, tokenHash).Scan(
		&t.ID, &t.UserID, &t.TokenHash, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

func (r *RefreshTokenRepo) RevokeByHash(ctx context.Context, tokenHash string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`,
		tokenHash)
	return err
}

// RevokeAllForUser is used on logout-all and admin deactivation.
func (r *RefreshTokenRepo) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`,
		userID)
	return err
}

// DeleteExpired purges rows that expired before cutoff. Returns rows removed.
func (r *RefreshTokenRepo) DeleteExpired(ctx context.Context, cutoff time.Time) (int64, error) {
	ct, err := r.db.Exec(ctx, `DELETE FROM refresh_tokens WHERE expires_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}
