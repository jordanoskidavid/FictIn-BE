package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRoleValid(t *testing.T) {
	tests := map[Role]bool{
		RoleAdmin:     true,
		RoleProfessor: true,
		RoleStudent:   true,
		"":            false,
		"Admin":       false,
		"teacher":     false,
	}
	for role, want := range tests {
		t.Run(string(role), func(t *testing.T) { assert.Equal(t, want, role.Valid()) })
	}
}
