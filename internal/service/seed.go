package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"fictin/internal/auth"
	"fictin/internal/models"
	"fictin/internal/repository"
)

// SeedAdmin creates the bootstrap admin from config if no admin exists yet.
// It is a no-op once any admin is present. Intended for development only.
func SeedAdmin(ctx context.Context, store *repository.Store, hasher *auth.Hasher, email, password string) error {
	exists, err := store.Users.ExistsAdmin(ctx)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || password == "" {
		return errors.New("admin seed email/password not configured")
	}
	hash, err := hasher.Hash(password)
	if err != nil {
		return err
	}
	_, err = store.Users.Create(ctx, repository.CreateUserParams{
		FullName:           "Bootstrap Admin",
		Email:              email,
		PasswordHash:       hash,
		Role:               models.RoleAdmin,
		MustChangePassword: false,
	})
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return nil // raced with another instance
		}
		return err
	}
	slog.Info("seeded bootstrap admin", "email", email)
	return nil
}
