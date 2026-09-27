package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidTime(t *testing.T) {
	tests := map[string]bool{
		"10:00":    true,
		"09:30:15": true,
		"23:59":    true,
		"00:00":    true,
		"24:00":    false,
		"9:30":     true, // single-digit hours parse; see known issue in schedules integration test
		"10:60":    false,
		"10h00":    false,
		"":         false,
	}
	for in, want := range tests {
		t.Run(in, func(t *testing.T) { assert.Equal(t, want, validTime(in)) })
	}
}
