package portalapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/internal/portalapi/auth"
)

// Task 7.3 — the admin role gate on GET /api/v1/admin/contributions, tested
// independently of the contribution DERIVATION (covered by contributions_test.go).
//
// handleGetContributions is gated EXACTLY like handleGetActivation: 403 unless
// hasMinRole(u.Role, "admin"). The auth middleware (auth_middleware.go) attaches
// the resolved auth.User under userContextKey{}; userFromRequest reads it back.
// There is no existing handler-gate test that injects a role, so these construct
// the minimal one: httptest.NewRequest + a context carrying the principal the way
// withAuth would, then call the handler method directly. This isolates the GATE
// (403 vs 200) from the OIDC validator and the full middleware chain.

// requestAs builds a GET request to path whose context carries the given auth
// principal under the same context key the auth middleware uses.
func requestAs(path string, u auth.User) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	ctx := context.WithValue(r.Context(), userContextKey{}, u)
	return r.WithContext(ctx)
}

// TestContributionsGate_ForbiddenForNonAdmin proves the gate rejects an
// unauthenticated (anonymous) principal AND a consumer principal with the exact
// writeError envelope {"error":"forbidden","message":"role 'admin' required"}
// and a 403, never reaching the derivation.
func TestContributionsGate_ForbiddenForNonAdmin(t *testing.T) {
	s := newContribServer(t)

	cases := map[string]auth.User{
		"anonymous": auth.Anonymous(),
		"consumer":  {Role: auth.RoleConsumer, Sub: "u1", Email: "dev@example.com"},
	}
	for name, principal := range cases {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.handleGetContributions(w, requestAs("/api/v1/admin/contributions?email=dev@example.com", principal))

			require.Equal(t, http.StatusForbidden, w.Code, "body=%s", w.Body.String())
			body := map[string]string{}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, "forbidden", body["error"])
			assert.Equal(t, "role 'admin' required", body["message"])
			// The gate must short-circuit: the forbidden envelope carries no
			// contributions payload.
			_, hasContribs := body["contributions"]
			assert.False(t, hasContribs, "forbidden response must not include the derived payload")
		})
	}
}

// TestContributionsGate_OKForAdmin proves an admin principal passes the gate and
// receives 200 with the {"email","contributions"} envelope.
func TestContributionsGate_OKForAdmin(t *testing.T) {
	s := newContribServer(t)

	w := httptest.NewRecorder()
	s.handleGetContributions(w, requestAs("/api/v1/admin/contributions?email=dev@example.com",
		auth.User{Role: auth.RoleAdmin, Sub: "admin1", Email: "admin@example.com"}))

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var body struct {
		Email         string         `json:"email"`
		Contributions []Contribution `json:"contributions"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "dev@example.com", body.Email, "echoes the requested email")
	// dev authored exactly the skill and the rule in the fixture (the derivation
	// itself is asserted in detail by contributions_test.go); here we only need
	// the admin to have passed the gate into a real 200 envelope.
	require.NotNil(t, body.Contributions)
	assert.Len(t, body.Contributions, 2)
}
