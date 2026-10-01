package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseTimeOfDay(t *testing.T) {
	tests := []struct {
		in   string
		ok   bool
		want string // normalized HH:MM:SS
	}{
		{"10:00", true, "10:00:00"},
		{"09:30:15", true, "09:30:15"},
		{"9:30", true, "09:30:00"},
		{"23:59:59", true, "23:59:59"},
		{"00:00", true, "00:00:00"},
		{"24:00", false, ""},
		{"10:60", false, ""},
		{"10h00", false, ""},
		{"", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := parseTimeOfDay(tt.in)
			assert.Equal(t, tt.ok, ok)
			if ok {
				assert.Equal(t, tt.want, got.Format(timeOfDayLayout))
			}
		})
	}
}

func TestParseTimeOfDayComparesAsTimes(t *testing.T) {
	early, _ := parseTimeOfDay("9:30")
	late, _ := parseTimeOfDay("10:00")
	same, _ := parseTimeOfDay("10:00:00")
	assert.True(t, late.After(early), "10:00 is after 9:30 (as text it sorts before)")
	assert.False(t, same.After(late), "10:00 and 10:00:00 are the same time")
}
