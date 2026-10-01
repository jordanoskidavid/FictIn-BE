package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"fictin/internal/apperr"
	"fictin/internal/httpresponse"
	"fictin/internal/models"
)

// AccountStates looks up the live state of the account behind a token
// (satisfied by *repository.UserRepo).
type AccountStates interface {
	AccountState(ctx context.Context, userID uuid.UUID) (models.AccountState, error)
}

// Middleware bundles the auth dependencies needed by HTTP middleware.
type Middleware struct {
	tokens   *TokenManager
	accounts AccountStates
}

// NewMiddleware builds the auth middleware. accounts is consulted on every
// authenticated request so deactivation and forced password changes apply
// immediately; nil skips the check (tests of token handling only).
func NewMiddleware(tokens *TokenManager, accounts AccountStates) *Middleware {
	return &Middleware{tokens: tokens, accounts: accounts}
}

// AuthRequired validates the bearer access token and injects the Identity.
//
// The token is read from the Authorization header, or (only useful for the
// WebSocket handshake, which browsers cannot send headers on) from the
// "access_token" query parameter.
func (m *Middleware) AuthRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			httpresponse.FromError(w, r, apperr.Unauthorized.WithDetails("missing bearer token"))
			return
		}
		claims, err := m.tokens.ParseAccessToken(raw)
		if err != nil {
			httpresponse.FromError(w, r, apperr.Unauthorized.WithCause(err))
			return
		}
		id := Identity{UserID: claims.UserID, Role: claims.Role, Email: claims.Email}
		if m.accounts != nil {
			state, err := m.accounts.AccountState(r.Context(), claims.UserID)
			if err != nil {
				httpresponse.FromError(w, r, apperr.Internal.WithCause(err))
				return
			}
			if !state.Exists || !state.Active {
				// 401 (not 403) so clients drop the token; refreshing fails too
				// and logging in again reports ACCOUNT_INACTIVE.
				httpresponse.FromError(w, r, apperr.Unauthorized.WithDetails("account is deactivated or no longer exists"))
				return
			}
			id.MustChangePassword = state.MustChangePassword
		}
		next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
	})
}

// RequirePasswordChanged blocks accounts that still have a temporary password
// (403 PASSWORD_CHANGE_REQUIRED). Mount it after AuthRequired on every route
// except the ones needed to change the password (/auth/me, /auth/change-password).
func (m *Middleware) RequirePasswordChanged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := FromContext(r.Context())
		if !ok {
			httpresponse.FromError(w, r, apperr.Unauthorized)
			return
		}
		if id.MustChangePassword {
			httpresponse.FromError(w, r, apperr.PasswordChangeRequired)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole allows the request through only if the caller's role is in allowed.
func (m *Middleware) RequireRole(allowed ...models.Role) func(http.Handler) http.Handler {
	set := make(map[models.Role]struct{}, len(allowed))
	for _, r := range allowed {
		set[r] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := FromContext(r.Context())
			if !ok {
				httpresponse.FromError(w, r, apperr.Unauthorized)
				return
			}
			if _, allowed := set[id.Role]; !allowed {
				httpresponse.FromError(w, r, apperr.Forbidden.WithDetails("insufficient role"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func bearerToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
			return strings.TrimSpace(h[7:])
		}
		return ""
	}
	return strings.TrimSpace(r.URL.Query().Get("access_token"))
}
