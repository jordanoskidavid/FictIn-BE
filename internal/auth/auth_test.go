package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"fictin/internal/models"
)

func TestPasswordHashAndCompare(t *testing.T) {
	h := NewHasher(4) // low cost for test speed
	hash, err := h.Hash("s3cret-pass")
	require.NoError(t, err)
	assert.NotEqual(t, "s3cret-pass", hash)
	assert.True(t, h.Compare(hash, "s3cret-pass"))
	assert.False(t, h.Compare(hash, "wrong"))
}

func TestAccessTokenRoundTrip(t *testing.T) {
	tm := NewTokenManager("test-secret", time.Minute, time.Hour)
	u := &models.User{ID: uuid.New(), Email: "a@b.c", Role: models.RoleProfessor}

	tok, exp, err := tm.GenerateAccessToken(u)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(time.Minute), exp, 5*time.Second)

	claims, err := tm.ParseAccessToken(tok)
	require.NoError(t, err)
	assert.Equal(t, u.ID, claims.UserID)
	assert.Equal(t, u.Email, claims.Email)
	assert.Equal(t, models.RoleProfessor, claims.Role)
}

func TestAccessTokenRejectsWrongSecret(t *testing.T) {
	a := NewTokenManager("secret-a", time.Minute, time.Hour)
	b := NewTokenManager("secret-b", time.Minute, time.Hour)
	u := &models.User{ID: uuid.New(), Email: "x@y.z", Role: models.RoleStudent}

	tok, _, err := a.GenerateAccessToken(u)
	require.NoError(t, err)

	_, err = b.ParseAccessToken(tok)
	assert.Error(t, err)
}

func TestAccessTokenExpired(t *testing.T) {
	tm := NewTokenManager("s", -time.Minute, time.Hour)
	u := &models.User{ID: uuid.New(), Email: "e@e.e", Role: models.RoleAdmin}
	tok, _, err := tm.GenerateAccessToken(u)
	require.NoError(t, err)
	_, err = tm.ParseAccessToken(tok)
	assert.Error(t, err)
}

func TestRefreshTokenHashDeterministic(t *testing.T) {
	tm := NewTokenManager("s", time.Minute, time.Hour)
	raw, hash, exp, err := tm.NewRefreshToken()
	require.NoError(t, err)
	assert.Equal(t, hash, HashRefreshToken(raw))
	assert.NotEqual(t, raw, hash)
	assert.True(t, exp.After(time.Now()))
}
