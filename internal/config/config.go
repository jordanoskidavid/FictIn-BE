package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all runtime configuration, loaded from environment variables
// (optionally seeded from a .env file in development).
type Config struct {
	AppEnv  string
	AppPort string

	DatabaseURL string

	JWTAccessSecret string
	JWTAccessTTL    time.Duration
	JWTRefreshTTL   time.Duration

	QRTokenTTL         time.Duration
	QRRotationInterval time.Duration

	// RequestTimeout bounds regular HTTP requests (not WebSocket streams).
	RequestTimeout time.Duration

	BcryptCost int

	AdminSeedEmail    string
	AdminSeedPassword string

	CORSAllowedOrigins []string

	ScanRateLimitPerSec float64
	ScanRateLimitBurst  int

	// TrustProxyHeaders honors X-Forwarded-For; enable only behind exactly one
	// reverse proxy that appends the client address.
	TrustProxyHeaders bool
}

// IsDevelopment reports whether the app is running in the development environment.
func (c *Config) IsDevelopment() bool {
	return strings.EqualFold(c.AppEnv, "development")
}

// Load reads configuration from the environment. If a .env file is present it is
// loaded first, but real environment variables always take precedence.
func Load() (*Config, error) {
	// Best-effort: ignore a missing .env file.
	_ = godotenv.Load()
	return Parse(os.LookupEnv)
}

// LookupFunc resolves one configuration key, like os.LookupEnv.
type LookupFunc func(key string) (string, bool)

// Parse builds and validates a Config from lookup. Tests use it with a map
// (e.g. loaded from .env.test) so they never mutate the process environment.
func Parse(lookup LookupFunc) (*Config, error) {
	cfg := &Config{
		AppEnv:             getEnv(lookup, "APP_ENV", "development"),
		AppPort:            getEnv(lookup, "APP_PORT", "8080"),
		DatabaseURL:        getEnv(lookup, "DATABASE_URL", ""),
		JWTAccessSecret:    getEnv(lookup, "JWT_ACCESS_SECRET", ""),
		AdminSeedEmail:     getEnv(lookup, "ADMIN_SEED_EMAIL", ""),
		AdminSeedPassword:  getEnv(lookup, "ADMIN_SEED_PASSWORD", ""),
		CORSAllowedOrigins: splitAndTrim(getEnv(lookup, "CORS_ALLOWED_ORIGINS", "*")),
	}

	accessMin, err := getEnvInt(lookup, "JWT_ACCESS_TTL_MINUTES", 30)
	if err != nil {
		return nil, err
	}
	cfg.JWTAccessTTL = time.Duration(accessMin) * time.Minute

	refreshHours, err := getEnvInt(lookup, "JWT_REFRESH_TTL_HOURS", 168)
	if err != nil {
		return nil, err
	}
	cfg.JWTRefreshTTL = time.Duration(refreshHours) * time.Hour

	tokenTTLMs, err := getEnvInt(lookup, "QR_TOKEN_TTL_MS", 1500)
	if err != nil {
		return nil, err
	}
	cfg.QRTokenTTL = time.Duration(tokenTTLMs) * time.Millisecond

	rotationMs, err := getEnvInt(lookup, "QR_ROTATION_INTERVAL_MS", 1000)
	if err != nil {
		return nil, err
	}
	cfg.QRRotationInterval = time.Duration(rotationMs) * time.Millisecond

	timeoutSec, err := getEnvInt(lookup, "HTTP_REQUEST_TIMEOUT_SECONDS", 30)
	if err != nil {
		return nil, err
	}
	cfg.RequestTimeout = time.Duration(timeoutSec) * time.Second

	cfg.BcryptCost, err = getEnvInt(lookup, "BCRYPT_COST", 12)
	if err != nil {
		return nil, err
	}

	cfg.ScanRateLimitPerSec, err = getEnvFloat(lookup, "SCAN_RATE_LIMIT_PER_SEC", 5)
	if err != nil {
		return nil, err
	}
	cfg.ScanRateLimitBurst, err = getEnvInt(lookup, "SCAN_RATE_LIMIT_BURST", 5)
	if err != nil {
		return nil, err
	}
	cfg.TrustProxyHeaders, err = getEnvBool(lookup, "TRUST_PROXY_HEADERS", false)
	if err != nil {
		return nil, err
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	var missing []string
	if c.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if c.JWTAccessSecret == "" {
		missing = append(missing, "JWT_ACCESS_SECRET")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	if c.BcryptCost < 4 || c.BcryptCost > 31 {
		return fmt.Errorf("BCRYPT_COST must be between 4 and 31, got %d", c.BcryptCost)
	}
	if c.IsDevelopment() && (c.AdminSeedEmail == "" || c.AdminSeedPassword == "") {
		return fmt.Errorf("ADMIN_SEED_EMAIL and ADMIN_SEED_PASSWORD are required in development")
	}
	return nil
}

func getEnv(lookup LookupFunc, key, fallback string) string {
	if v, ok := lookup(key); ok && v != "" {
		return v
	}
	return fallback
}

func getEnvInt(lookup LookupFunc, key string, fallback int) (int, error) {
	v, ok := lookup(key)
	if !ok || v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("invalid integer for %s: %q", key, v)
	}
	return n, nil
}

func getEnvBool(lookup LookupFunc, key string, fallback bool) (bool, error) {
	v, ok := lookup(key)
	if !ok || v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return false, fmt.Errorf("invalid boolean for %s: %q", key, v)
	}
	return b, nil
}

func getEnvFloat(lookup LookupFunc, key string, fallback float64) (float64, error) {
	v, ok := lookup(key)
	if !ok || v == "" {
		return fallback, nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid float for %s: %q", key, v)
	}
	return f, nil
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
