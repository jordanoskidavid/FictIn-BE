package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"fictin/internal/apperr"
	"fictin/internal/auth"
	"fictin/internal/models"
	"fictin/internal/repository"
)

type AuthService struct {
	store  *repository.Store
	hasher *auth.Hasher
	tokens *auth.TokenManager
}

func NewAuthService(store *repository.Store, hasher *auth.Hasher, tokens *auth.TokenManager) *AuthService {
	return &AuthService{store: store, hasher: hasher, tokens: tokens}
}

type RegisterInput struct {
	FullName    string
	Email       string
	Password    string
	Role        models.Role
	StudentCode *string
}

// Register creates a user. Callable only from an admin-guarded handler.
func (s *AuthService) Register(ctx context.Context, in RegisterInput) (*models.User, error) {
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if !in.Role.Valid() {
		return nil, apperr.Validation.WithDetails("role must be one of admin, professor, student")
	}
	if in.Role == models.RoleStudent {
		if in.StudentCode == nil || strings.TrimSpace(*in.StudentCode) == "" {
			return nil, apperr.Validation.WithDetails("student_code is required for students")
		}
		trimmed := strings.TrimSpace(*in.StudentCode)
		in.StudentCode = &trimmed
	} else {
		in.StudentCode = nil // ignore for non-students
	}

	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}

	u, err := s.store.Users.Create(ctx, repository.CreateUserParams{
		FullName:           strings.TrimSpace(in.FullName),
		Email:              in.Email,
		PasswordHash:       hash,
		Role:               in.Role,
		StudentCode:        in.StudentCode,
		MustChangePassword: true, // v1: admin sets a temp password, user must change it
	})
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			if strings.Contains(err.Error(), "student_code") {
				return nil, apperr.Conflict.WithDetails("student_code already in use")
			}
			return nil, apperr.Conflict.WithDetails("email already in use")
		}
		return nil, apperr.Internal.WithCause(err)
	}
	return u, nil
}

type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresInSec int
	User         *models.User
}

func (s *AuthService) Login(ctx context.Context, email, password string) (*TokenPair, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	u, err := s.store.Users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// Uniform message; still run a hash compare to reduce timing signal.
			_ = s.hasher.Compare("$2a$12$invalidinvalidinvalidinvalidinvalidinvalidinvalidinva", password)
			return nil, apperr.InvalidCreds
		}
		return nil, apperr.Internal.WithCause(err)
	}
	if !s.hasher.Compare(u.PasswordHash, password) {
		return nil, apperr.InvalidCreds
	}
	if !u.IsActive {
		return nil, apperr.AccountInactive
	}
	return s.issuePair(ctx, u, uuid.New()) // a login starts a new token family
}

// Refresh rotates a refresh token: the presented token is consumed atomically
// and a new pair in the same family is issued. Presenting a token that was
// already rotated means a copy is in someone else's hands, so the whole
// family is revoked (the attacker and the victim must both log in again).
func (s *AuthService) Refresh(ctx context.Context, rawRefresh string) (*TokenPair, error) {
	invalid := apperr.Unauthorized.WithDetails("refresh token invalid or expired")
	hash := auth.HashRefreshToken(strings.TrimSpace(rawRefresh))

	rt, err := s.store.Refresh.Consume(ctx, hash)
	if errors.Is(err, repository.ErrNotFound) {
		old, lookupErr := s.store.Refresh.GetByHash(ctx, hash)
		if lookupErr == nil && old.RevokedAt != nil {
			n, revokeErr := s.store.Refresh.RevokeFamily(ctx, old.FamilyID)
			if revokeErr != nil {
				return nil, apperr.Internal.WithCause(revokeErr)
			}
			slog.WarnContext(ctx, "refresh token reuse detected; family revoked",
				"user_id", old.UserID, "family_id", old.FamilyID, "revoked", n)
		}
		return nil, invalid
	}
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}

	u, err := s.store.Users.GetByID(ctx, rt.UserID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, invalid
		}
		return nil, apperr.Internal.WithCause(err)
	}
	if !u.IsActive {
		_ = s.store.Refresh.RevokeAllForUser(ctx, u.ID)
		return nil, apperr.AccountInactive
	}
	return s.issuePair(ctx, u, rt.FamilyID)
}

func (s *AuthService) Logout(ctx context.Context, rawRefresh string) error {
	hash := auth.HashRefreshToken(strings.TrimSpace(rawRefresh))
	if err := s.store.Refresh.RevokeByHash(ctx, hash); err != nil {
		return apperr.Internal.WithCause(err)
	}
	return nil
}

func (s *AuthService) Me(ctx context.Context, userID uuid.UUID) (*models.User, error) {
	u, err := s.store.Users.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, apperr.NotFound.WithDetails("user not found")
		}
		return nil, apperr.Internal.WithCause(err)
	}
	return u, nil
}

func (s *AuthService) ChangePassword(ctx context.Context, userID uuid.UUID, current, next string) error {
	u, err := s.store.Users.GetByID(ctx, userID)
	if err != nil {
		return apperr.Internal.WithCause(err)
	}
	if !s.hasher.Compare(u.PasswordHash, current) {
		return apperr.InvalidCreds.WithDetails("current password is incorrect")
	}
	if current == next {
		return apperr.Validation.WithDetails("new password must differ from the current password")
	}
	hash, err := s.hasher.Hash(next)
	if err != nil {
		return apperr.Internal.WithCause(err)
	}
	if err := s.store.Users.SetPassword(ctx, userID, hash, false); err != nil {
		return apperr.Internal.WithCause(err)
	}
	// Force re-login everywhere after a password change.
	_ = s.store.Refresh.RevokeAllForUser(ctx, userID)
	return nil
}

func (s *AuthService) issuePair(ctx context.Context, u *models.User, familyID uuid.UUID) (*TokenPair, error) {
	access, _, err := s.tokens.GenerateAccessToken(u)
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}
	raw, hash, expiresAt, err := s.tokens.NewRefreshToken()
	if err != nil {
		return nil, apperr.Internal.WithCause(err)
	}
	if err := s.store.Refresh.Create(ctx, u.ID, familyID, hash, expiresAt); err != nil {
		return nil, apperr.Internal.WithCause(err)
	}
	return &TokenPair{
		AccessToken:  access,
		RefreshToken: raw,
		ExpiresInSec: int(s.tokens.AccessTTL().Seconds()),
		User:         u,
	}, nil
}
