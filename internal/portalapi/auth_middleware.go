package portalapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/forge/fdh/internal/portalapi/auth"
)

// withAuth attaches an `auth.User` to the request context for every request.
// When the server has no validator configured (cfg.AuthEnabled == false),
// every request is anonymous. When a bearer token is present and valid, the
// resolved user replaces the anonymous default. Invalid tokens return 401.
//
// Missing tokens are NOT a 401 — anonymous routes (catalog) must still work
// without authentication. Handlers that require a role check it explicitly.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := auth.Anonymous()

		raw := auth.ExtractBearer(r)
		if raw != "" {
			if s.validator == nil {
				// A token was presented but auth is disabled. Reject loudly
				// so misconfiguration is visible.
				s.writeError(w, http.StatusUnauthorized, "unauthorized",
					"bearer token presented but OIDC auth is not configured on this server")
				return
			}
			u, err := s.validator.Validate(r.Context(), raw)
			if err != nil {
				// The IdP being unreachable is a retryable server condition,
				// not a bad token: surface 503 so clients (and the token's
				// holder) retry rather than treating the token as invalid.
				if errors.Is(err, auth.ErrAuthUnavailable) {
					w.Header().Set("Retry-After", "10")
					s.writeError(w, http.StatusServiceUnavailable, "auth_unavailable",
						"identity provider is temporarily unreachable; retry shortly")
					return
				}
				s.writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
				return
			}
			user = u
		}

		ctx := context.WithValue(r.Context(), userContextKey{}, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// userFromRequest pulls the auth.User off the request context. Defaults to
// anonymous if no auth middleware ran.
func userFromRequest(r *http.Request) auth.User {
	if u, ok := r.Context().Value(userContextKey{}).(auth.User); ok {
		return u
	}
	return auth.Anonymous()
}
