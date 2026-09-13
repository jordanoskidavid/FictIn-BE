package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"fictin/internal/models"
)

// AccessClaims is the JWT payload for access tokens.
type AccessClaims struct {
	UserID uuid.UUID   `json:"user_id"`
	Role   models.Role `json:"role"`
	Email  string      `json:"email"`
	jwt.RegisteredClaims
}

// TokenManager issues and verifies access tokens and mints opaque refresh tokens.
type TokenManager struct {
	accessSecret []byte
	accessTTL    time.Duration
	refreshTTL   time.Duration
}

func NewTokenManager(accessSecret string, accessTTL, refreshTTL time.Duration) *TokenManager {
	return &TokenManager{
		accessSecret: []byte(accessSecret),
		accessTTL:    accessTTL,
		refreshTTL:   refreshTTL,
	}
}

func (m *TokenManager) AccessTTL() time.Duration  { return m.accessTTL }
func (m *TokenManager) RefreshTTL() time.Duration { return m.refreshTTL }

// GenerateAccessToken returns a signed JWT and its expiry.
func (m *TokenManager) GenerateAccessToken(u *models.User) (string, time.Time, error) {
	now := time.Now().UTC()
	exp := now.Add(m.accessTTL)
	claims := AccessClaims{
		UserID: u.ID,
		Role:   u.Role,
		Email:  u.Email,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   u.ID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(m.accessSecret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, exp, nil
}

// ParseAccessToken validates the signature and expiry and returns the claims.
func (m *TokenManager) ParseAccessToken(raw string) (*AccessClaims, error) {
	claims := &AccessClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return m.accessSecret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, err
	}
	if claims.UserID == uuid.Nil {
		return nil, errors.New("token missing user_id")
	}
	return claims, nil
}

// NewRefreshToken returns a fresh opaque refresh token (raw), its storage hash,
// and its expiry timestamp. Only the hash is persisted.
func (m *TokenManager) NewRefreshToken() (raw, hash string, expiresAt time.Time, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", time.Time{}, err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	hash = HashRefreshToken(raw)
	expiresAt = time.Now().UTC().Add(m.refreshTTL)
	return raw, hash, expiresAt, nil
}

// HashRefreshToken returns the hex sha-256 of a raw refresh token.
func HashRefreshToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
