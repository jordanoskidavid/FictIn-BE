package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lookupFrom(env map[string]string) LookupFunc {
	return func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
}

func validEnv() map[string]string {
	return map[string]string{
		"APP_ENV":           "production",
		"DATABASE_URL":      "postgres://u:p@localhost/db",
		"JWT_ACCESS_SECRET": "a",
	}
}

func TestParseDefaults(t *testing.T) {
	cfg, err := Parse(lookupFrom(validEnv()))
	require.NoError(t, err)

	assert.Equal(t, "8080", cfg.AppPort)
	assert.Equal(t, 30*time.Minute, cfg.JWTAccessTTL)
	assert.Equal(t, 168*time.Hour, cfg.JWTRefreshTTL)
	assert.Equal(t, 1500*time.Millisecond, cfg.QRTokenTTL)
	assert.Equal(t, time.Second, cfg.QRRotationInterval)
	assert.Equal(t, 30*time.Second, cfg.RequestTimeout)
	assert.Equal(t, 12, cfg.BcryptCost)
	assert.Equal(t, []string{"*"}, cfg.CORSAllowedOrigins)
	assert.Equal(t, 5.0, cfg.ScanRateLimitPerSec)
	assert.Equal(t, 5, cfg.ScanRateLimitBurst)
	assert.False(t, cfg.IsDevelopment())
}

func TestParseOverrides(t *testing.T) {
	env := validEnv()
	env["QR_TOKEN_TTL_MS"] = "2000"
	env["JWT_ACCESS_TTL_MINUTES"] = " 15 "
	env["CORS_ALLOWED_ORIGINS"] = "https://a.example, https://b.example ,"
	env["SCAN_RATE_LIMIT_PER_SEC"] = "2.5"

	cfg, err := Parse(lookupFrom(env))
	require.NoError(t, err)
	assert.Equal(t, 2*time.Second, cfg.QRTokenTTL)
	assert.Equal(t, 15*time.Minute, cfg.JWTAccessTTL)
	assert.Equal(t, []string{"https://a.example", "https://b.example"}, cfg.CORSAllowedOrigins)
	assert.Equal(t, 2.5, cfg.ScanRateLimitPerSec)
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(map[string]string)
		wantErr string
	}{
		{"missing database url", func(e map[string]string) { delete(e, "DATABASE_URL") }, "DATABASE_URL"},
		{"missing access secret and database url", func(e map[string]string) { delete(e, "JWT_ACCESS_SECRET"); delete(e, "DATABASE_URL") }, "DATABASE_URL, JWT_ACCESS_SECRET"},
		{"empty value counts as missing", func(e map[string]string) { e["JWT_ACCESS_SECRET"] = "" }, "JWT_ACCESS_SECRET"},
		{"non-numeric int", func(e map[string]string) { e["QR_TOKEN_TTL_MS"] = "fast" }, "QR_TOKEN_TTL_MS"},
		{"non-numeric float", func(e map[string]string) { e["SCAN_RATE_LIMIT_PER_SEC"] = "x" }, "SCAN_RATE_LIMIT_PER_SEC"},
		{"bcrypt cost too low", func(e map[string]string) { e["BCRYPT_COST"] = "3" }, "BCRYPT_COST"},
		{"bcrypt cost too high", func(e map[string]string) { e["BCRYPT_COST"] = "32" }, "BCRYPT_COST"},
		{"development requires seed admin", func(e map[string]string) { e["APP_ENV"] = "Development" }, "ADMIN_SEED_EMAIL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnv()
			tt.mutate(env)
			_, err := Parse(lookupFrom(env))
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// Refresh tokens are opaque values stored hashed in the database, so no
// refresh signing secret is needed; an old JWT_REFRESH_SECRET is ignored.
func TestParseDoesNotRequireRefreshSecret(t *testing.T) {
	env := validEnv()
	_, ok := env["JWT_REFRESH_SECRET"]
	require.False(t, ok)
	_, err := Parse(lookupFrom(env))
	require.NoError(t, err)

	env["JWT_REFRESH_SECRET"] = "legacy"
	_, err = Parse(lookupFrom(env))
	require.NoError(t, err)
}
