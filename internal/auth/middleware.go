package auth

import (
	"net/http"
	"strings"

	"fictin/internal/apperr"
	"fictin/internal/httpresponse"
	"fictin/internal/models"
)

// Middleware bundles the auth dependencies needed by HTTP middleware.
type Middleware struct {
	tokens *TokenManager
}

func NewMiddleware(tokens *TokenManager) *Middleware {
	return &Middleware{tokens: tokens}
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
		next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
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
