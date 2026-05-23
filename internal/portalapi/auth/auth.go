// Package auth implements OIDC token validation and portal role mapping
// for the FDH portal API.
//
// The package is small and focused: it takes a bearer token, validates it
// against an IdP's JWKS, and returns a portal role derived from a
// configurable claim → role map. There is no session state — every request
// is validated independently. JWKS keys are cached by the go-oidc library
// with kid-based rotation.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"gopkg.in/yaml.v3"
)

// Role precedence: anonymous < consumer < author < reviewer < publisher < admin.
const (
	RoleAnonymous = "anonymous"
	RoleConsumer  = "consumer"
	RoleAuthor    = "author"
	RoleReviewer  = "reviewer"
	RolePublisher = "publisher"
	RoleAdmin     = "admin"
)

// RoleRank returns the numeric precedence of a role (higher = more privileged).
// Unknown roles map to 0 (anonymous).
func RoleRank(role string) int {
	switch role {
	case RoleAdmin:
		return 5
	case RolePublisher:
		return 4
	case RoleReviewer:
		return 3
	case RoleAuthor:
		return 2
	case RoleConsumer:
		return 1
	}
	return 0
}

// HasMinRole reports whether `actual` satisfies the minimum `required` role.
func HasMinRole(actual, required string) bool {
	return RoleRank(actual) >= RoleRank(required)
}

// User is the authenticated principal attached to a request context.
type User struct {
	Role   string
	Sub    string
	Name   string
	Email  string
	Claims []string
}

// Anonymous returns a User with role=anonymous and no identity fields.
func Anonymous() User {
	return User{Role: RoleAnonymous}
}

// RoleMap maps claim values to portal roles. A claim value (e.g. a Keycloak
// group name or an Entra ID role id) appears in `Map`; the value is the
// portal role it grants. Unmapped claims do not grant any role. A user with
// no mapped roles defaults to `consumer` after successful authentication.
type RoleMap struct {
	// Claim is the JWT claim name to read (default "groups").
	Claim string `yaml:"claim"`
	// Map is the claim-value → portal-role lookup.
	Map map[string]string `yaml:"map"`
}

// LoadRoleMap reads a YAML file into a RoleMap. An empty path returns an
// empty map (every authenticated user is `consumer`).
func LoadRoleMap(path string) (RoleMap, error) {
	if strings.TrimSpace(path) == "" {
		return RoleMap{Claim: "groups", Map: map[string]string{}}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return RoleMap{}, fmt.Errorf("read role map %s: %w", path, err)
	}
	var rm RoleMap
	if err := yaml.Unmarshal(data, &rm); err != nil {
		return RoleMap{}, fmt.Errorf("parse role map %s: %w", path, err)
	}
	if rm.Claim == "" {
		rm.Claim = "groups"
	}
	if rm.Map == nil {
		rm.Map = map[string]string{}
	}
	return rm, nil
}

// Validator validates bearer tokens against an OIDC IdP and resolves
// portal roles.
type Validator struct {
	verifier *oidc.IDTokenVerifier
	roleMap  RoleMap
}

// New constructs a Validator. The discoveryURL is the IdP's well-known
// configuration URL; clientID is the audience the API expects.
// A zero clientID means audience validation is skipped (development only).
func New(ctx context.Context, discoveryURL, clientID string, roleMap RoleMap) (*Validator, error) {
	if discoveryURL == "" {
		return nil, errors.New("auth.New: discoveryURL is required")
	}
	provider, err := oidc.NewProvider(ctx, discoveryURL)
	if err != nil {
		return nil, fmt.Errorf("oidc provider: %w", err)
	}
	verifierConfig := &oidc.Config{
		ClientID:          clientID,
		SkipClientIDCheck: clientID == "",
	}
	return &Validator{
		verifier: provider.Verifier(verifierConfig),
		roleMap:  roleMap,
	}, nil
}

// Validate parses + verifies a raw JWT and returns the User. The role is
// derived from the role map; if no claim values map, the user is
// `consumer` (authenticated but without elevated role).
func (v *Validator) Validate(ctx context.Context, rawToken string) (User, error) {
	idt, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return User{}, fmt.Errorf("verify token: %w", err)
	}
	var raw map[string]any
	if err := idt.Claims(&raw); err != nil {
		return User{}, fmt.Errorf("read claims: %w", err)
	}

	user := User{
		Sub:    idt.Subject,
		Role:   RoleConsumer, // default after authentication
		Claims: extractClaimValues(raw, v.roleMap.Claim),
	}
	if s, ok := raw["name"].(string); ok {
		user.Name = s
	}
	if s, ok := raw["email"].(string); ok {
		user.Email = s
	}

	// Resolve role precedence over every mapped claim value.
	for _, c := range user.Claims {
		if r, ok := v.roleMap.Map[c]; ok {
			if RoleRank(r) > RoleRank(user.Role) {
				user.Role = r
			}
		}
	}
	return user, nil
}

// extractClaimValues pulls the string values from a claim that may be a
// single string, a []string, or a []any. Returns an empty slice for
// missing or wrongly-typed claims.
func extractClaimValues(raw map[string]any, claim string) []string {
	v, ok := raw[claim]
	if !ok {
		return nil
	}
	switch typed := v.(type) {
	case string:
		return []string{typed}
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// ExtractBearer parses the Authorization header and returns the raw JWT.
// Returns "" when no bearer token is present.
func ExtractBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}
