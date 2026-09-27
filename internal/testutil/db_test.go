package testutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckTestPostgresURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"database name contains test", "postgres://app:pw@localhost:5432/attendance_test_db", false},
		{"user contains test", "postgres://attendance_test:pw@localhost:5432/postgres", false},
		{"case insensitive", "postgresql://CI_TEST:pw@db:5432/postgres?sslmode=disable", false},
		{"dev database refused", "postgres://attendance_app:pw@localhost:5432/attendance_db", true},
		{"production-looking database refused", "postgres://admin:pw@prod.example.com:5432/attendance", true},
		{"test only in password refused", "postgres://app:test@localhost:5432/attendance_db", true},
		{"test only in host refused", "postgres://app:pw@test-host:5432/attendance_db", true},
		{"key=value DSN refused", "host=localhost user=test dbname=test", true},
		{"garbage refused", "postgres://%%%", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckTestPostgresURL(tt.url)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestWithDatabase(t *testing.T) {
	got := withDatabase("postgres://u:p@localhost:5432/postgres?sslmode=disable", "fictin_test_abc")
	assert.Equal(t, "postgres://u:p@localhost:5432/fictin_test_abc?sslmode=disable", got)
}

func TestDropDatabaseRefusesForeignNames(t *testing.T) {
	err := dropDatabase(t.Context(), "postgres://unused", "attendance_db")
	assert.ErrorContains(t, err, "refusing to drop")
}
