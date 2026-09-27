package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"fictin/internal/clock"
	"fictin/internal/models"
)

var t0 = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

func testUser(role models.Role) *models.User {
	return &models.User{ID: uuid.New(), Email: "user@example.test", Role: role}
}

func TestHasher(t *testing.T) {
	h := NewHasher(bcrypt.MinCost)
	hash, err := h.Hash("s3cret-pass")
	require.NoError(t, err)

	tests := []struct {
		name  string
		plain string
		want  bool
	}{
		{"correct password", "s3cret-pass", true},
		{"wrong password", "wrong", false},
		{"empty password", "", false},
		{"case sensitive", "S3CRET-PASS", false},
		{"prefix only", "s3cret", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, h.Compare(hash, tt.plain))
		})
	}

	t.Run("hash is salted and never the plaintext", func(t *testing.T) {
		again, err := h.Hash("s3cret-pass")
		require.NoError(t, err)
		assert.NotEqual(t, "s3cret-pass", hash)
		assert.NotEqual(t, hash, again)
	})
	t.Run("garbage hash never matches", func(t *testing.T) {
		assert.False(t, h.Compare("not-a-bcrypt-hash", "s3cret-pass"))
	})
}

func TestNewHasherCost(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{"valid cost kept", 5, 5},
		{"below minimum falls back to default", 1, bcrypt.DefaultCost},
		{"above maximum falls back to default", 99, bcrypt.DefaultCost},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NewHasher(tt.in).cost)
		})
	}
}

func TestAccessTokenRoundTrip(t *testing.T) {
	clk := clock.NewFake(t0)
	tm := NewTokenManager("test-secret", 15*time.Minute, time.Hour, WithClock(clk))

	for _, role := range []models.Role{models.RoleAdmin, models.RoleProfessor, models.RoleStudent} {
		t.Run(string(role), func(t *testing.T) {
			u := testUser(role)
			tok, exp, err := tm.GenerateAccessToken(u)
			require.NoError(t, err)
			assert.Equal(t, t0.Add(15*time.Minute), exp)

			claims, err := tm.ParseAccessToken(tok)
			require.NoError(t, err)
			assert.Equal(t, u.ID, claims.UserID)
			assert.Equal(t, u.Email, claims.Email)
			assert.Equal(t, role, claims.Role)
			assert.Equal(t, u.ID.String(), claims.Subject)
			assert.Equal(t, t0, claims.IssuedAt.UTC())
		})
	}
}

func TestAccessTokenExpiry(t *testing.T) {
	tests := []struct {
		name    string
		advance time.Duration
		wantErr bool
	}{
		{"fresh token", 0, false},
		{"one second before expiry", 15*time.Minute - time.Second, false},
		{"exactly at expiry", 15 * time.Minute, true},
		{"after expiry", 15*time.Minute + time.Second, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clk := clock.NewFake(t0)
			tm := NewTokenManager("s", 15*time.Minute, time.Hour, WithClock(clk))
			tok, _, err := tm.GenerateAccessToken(testUser(models.RoleStudent))
			require.NoError(t, err)

			clk.Advance(tt.advance)
			_, err = tm.ParseAccessToken(tok)
			if tt.wantErr {
				assert.ErrorIs(t, err, jwt.ErrTokenExpired)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAccessTokenRejectsForgedTokens(t *testing.T) {
	tm := NewTokenManager("secret-a", time.Minute, time.Hour)
	valid, _, err := tm.GenerateAccessToken(testUser(models.RoleStudent))
	require.NoError(t, err)

	sign := func(method jwt.SigningMethod, key any, claims jwt.Claims) string {
		s, err := jwt.NewWithClaims(method, claims).SignedString(key)
		require.NoError(t, err)
		return s
	}
	future := jwt.NewNumericDate(time.Now().Add(time.Hour))
	goodClaims := AccessClaims{UserID: uuid.New(), Role: models.RoleAdmin, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: future}}

	parts := strings.Split(valid, ".")
	tampered := parts[0] + "." + parts[1] + "x." + parts[2]

	tests := []struct {
		name  string
		token string
	}{
		{"signed with another secret", sign(jwt.SigningMethodHS256, []byte("secret-b"), goodClaims)},
		{"alg none", sign(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, goodClaims)},
		{"HS512 not allowed", sign(jwt.SigningMethodHS512, []byte("secret-a"), goodClaims)},
		{"missing user_id", sign(jwt.SigningMethodHS256, []byte("secret-a"), AccessClaims{Role: models.RoleAdmin, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: future}})},
		{"tampered payload", tampered},
		{"not a jwt", "definitely-not-a-jwt"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tm.ParseAccessToken(tt.token)
			assert.Error(t, err)
		})
	}
}

func TestRefreshToken(t *testing.T) {
	clk := clock.NewFake(t0)
	tm := NewTokenManager("s", time.Minute, 7*24*time.Hour, WithClock(clk))

	raw, hash, exp, err := tm.NewRefreshToken()
	require.NoError(t, err)
	assert.Equal(t, hash, HashRefreshToken(raw), "stored hash must be derivable from the raw token")
	assert.NotEqual(t, raw, hash, "raw token must never be stored")
	assert.Len(t, hash, 64, "hex sha-256")
	assert.Equal(t, t0.Add(7*24*time.Hour), exp)

	raw2, _, _, err := tm.NewRefreshToken()
	require.NoError(t, err)
	assert.NotEqual(t, raw, raw2, "tokens must be random")
}
