package auth

import (
	"context"

	"github.com/google/uuid"

	"fictin/internal/models"
)

type ctxKey int

const identityKey ctxKey = iota

// Identity is the authenticated caller, injected by AuthRequired.
type Identity struct {
	UserID uuid.UUID
	Role   models.Role
	Email  string

	// MustChangePassword is the live account flag (temporary password).
	MustChangePassword bool
}

func (i Identity) IsAdmin() bool     { return i.Role == models.RoleAdmin }
func (i Identity) IsProfessor() bool { return i.Role == models.RoleProfessor }
func (i Identity) IsStudent() bool   { return i.Role == models.RoleStudent }

// WithIdentity returns a child context carrying the identity.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey, id)
}

// FromContext extracts the identity; ok is false if the request was not authenticated.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey).(Identity)
	return id, ok
}

// MustIdentity returns the identity or panics — only use inside handlers mounted
// behind AuthRequired.
func MustIdentity(ctx context.Context) Identity {
	id, ok := FromContext(ctx)
	if !ok {
		panic("auth.MustIdentity: no identity in context (missing AuthRequired middleware?)")
	}
	return id
}
