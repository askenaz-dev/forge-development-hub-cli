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
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"gopkg.in/yaml.v3"
)

// ErrAuthUnavailable signals that the IdP's discovery endpoint could not be
// reached, so a presented token could not be validated. It is distinct from
// an invalid-token error: the token may be perfectly valid; the server simply
// cannot verify it right now. Callers should map it to 503 (retryable), not
// 401, and MUST keep anonymous endpoints serving.
var ErrAuthUnavailable = errors.New("auth temporarily unavailable")

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

// LazyValidator defers OIDC discovery until the first time a token must be
// validated, retrying on each attempt until the IdP is reachable. This
// decouples API startup from IdP availability: the anonymous catalog stays
// up even when the IdP is unreachable at boot, and token validation begins
// working automatically once the IdP recovers — with no process restart.
//
// Rationale: the portal API is a read-only, anonymous-by-default catalog.
// Constructing the validator eagerly at startup (and treating failure as
// fatal) coupled the entire catalog's availability to the IdP being healthy
// at that instant — a transient outage or a wiped realm crash-looped the API
// and 503'd every endpoint. A successfully constructed inner Validator is
// cached; construction failures are NOT cached, so the next call retries.
type LazyValidator struct {
	discoveryURL string
	clientID     string
	roleMap      RoleMap

	mu    sync.Mutex
	inner *Validator
}

// NewLazy returns a LazyValidator. It performs no network I/O; the IdP is
// contacted lazily on first use (or eagerly, best-effort, via Warm).
func NewLazy(discoveryURL, clientID string, roleMap RoleMap) *LazyValidator {
	return &LazyValidator{
		discoveryURL: discoveryURL,
		clientID:     clientID,
		roleMap:      roleMap,
	}
}

// Warm attempts to construct the inner Validator now. A nil return means auth
// is ready; a non-nil error means the IdP was unreachable and the
// LazyValidator will retry on the next Validate. Intended for a best-effort
// warm-up at startup that never fails the boot path.
func (l *LazyValidator) Warm(ctx context.Context) error {
	_, err := l.ensure(ctx)
	return err
}

// Ready reports whether the inner Validator has been constructed.
func (l *LazyValidator) Ready() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inner != nil
}

// ensure returns a ready inner Validator, constructing and caching it on the
// first success. Discovery is bounded by a 10s timeout so a hung IdP cannot
// block token validation indefinitely while the lock is held.
func (l *LazyValidator) ensure(ctx context.Context) (*Validator, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inner != nil {
		return l.inner, nil
	}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	v, err := New(dctx, l.discoveryURL, l.clientID, l.roleMap)
	if err != nil {
		return nil, err
	}
	l.inner = v
	return v, nil
}

// Validate lazily initializes the underlying OIDC verifier, then validates
// the token. When the IdP is unreachable it returns an error wrapping
// ErrAuthUnavailable so callers can distinguish "cannot verify" (503) from
// "token invalid" (401).
func (l *LazyValidator) Validate(ctx context.Context, rawToken string) (User, error) {
	v, err := l.ensure(ctx)
	if err != nil {
		return User{}, fmt.Errorf("%w: %v", ErrAuthUnavailable, err)
	}
	return v.Validate(ctx, rawToken)
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
