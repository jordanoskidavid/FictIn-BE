package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fictin/internal/models"
)

type QRTokenRepo struct{ db DBTX }

func (r *QRTokenRepo) Insert(ctx context.Context, sessionID uuid.UUID, token string, issuedAt, expiresAt time.Time) (*models.QRToken, error) {
	const q = `INSERT INTO qr_tokens (session_id, token, issued_at, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, session_id, token, issued_at, expires_at`
	var t models.QRToken
	err := r.db.QueryRow(ctx, q, sessionID, token, issuedAt, expiresAt).
		Scan(&t.ID, &t.SessionID, &t.Token, &t.IssuedAt, &t.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetByToken looks up a token by value, regardless of session or expiry. The
// caller decides validity (service.checkScanToken) so it can use an injected clock.
func (r *QRTokenRepo) GetByToken(ctx context.Context, token string) (*models.QRToken, error) {
	const q = `SELECT id, session_id, token, issued_at, expires_at
		FROM qr_tokens WHERE token = $1`
	var t models.QRToken
	err := r.db.QueryRow(ctx, q, token).
		Scan(&t.ID, &t.SessionID, &t.Token, &t.IssuedAt, &t.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

// Latest returns the most recently issued token for a session (polling fallback).
func (r *QRTokenRepo) Latest(ctx context.Context, sessionID uuid.UUID) (*models.QRToken, error) {
	const q = `SELECT id, session_id, token, issued_at, expires_at
		FROM qr_tokens WHERE session_id = $1
		ORDER BY issued_at DESC LIMIT 1`
	var t models.QRToken
	err := r.db.QueryRow(ctx, q, sessionID).
		Scan(&t.ID, &t.SessionID, &t.Token, &t.IssuedAt, &t.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

func (r *QRTokenRepo) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	ct, err := r.db.Exec(ctx, `DELETE FROM qr_tokens WHERE issued_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}
