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

// Create stores a refresh token (by hash) in the given family. A login starts
// a new family; each refresh adds the next token to the same family.
func (r *RefreshTokenRepo) Create(ctx context.Context, userID, familyID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at) VALUES ($1, $2, $3, $4)`,
		userID, familyID, tokenHash, expiresAt)
	return err
}

const refreshCols = `id, user_id, family_id, token_hash, expires_at, revoked_at, created_at`

func scanRefresh(row pgx.Row) (*models.RefreshToken, error) {
	var t models.RefreshToken
	err := row.Scan(&t.ID, &t.UserID, &t.FamilyID, &t.TokenHash, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

// GetActiveByHash returns a non-revoked, non-expired token row.
func (r *RefreshTokenRepo) GetActiveByHash(ctx context.Context, tokenHash string) (*models.RefreshToken, error) {
	return scanRefresh(r.db.QueryRow(ctx, `SELECT `+refreshCols+` FROM refresh_tokens
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()`, tokenHash))
}

// GetByHash returns a token row in any state (used to detect reuse).
func (r *RefreshTokenRepo) GetByHash(ctx context.Context, tokenHash string) (*models.RefreshToken, error) {
	return scanRefresh(r.db.QueryRow(ctx, `SELECT `+refreshCols+` FROM refresh_tokens WHERE token_hash = $1`, tokenHash))
}

// Consume atomically revokes an active (non-revoked, unexpired) token and
// returns it. Of several concurrent calls with the same token exactly one
// succeeds; the others get ErrNotFound.
func (r *RefreshTokenRepo) Consume(ctx context.Context, tokenHash string) (*models.RefreshToken, error) {
	return scanRefresh(r.db.QueryRow(ctx, `UPDATE refresh_tokens SET revoked_at = now()
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
		RETURNING `+refreshCols, tokenHash))
}

// RevokeFamily revokes every still-active token of a login chain.
func (r *RefreshTokenRepo) RevokeFamily(ctx context.Context, familyID uuid.UUID) (int64, error) {
	ct, err := r.db.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL`, familyID)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
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
