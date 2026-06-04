package portalapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/internal/portalapi"
	"github.com/forge/fdh/internal/testutil"
)

// TestNew_OIDCUnreachableDoesNotCrash is the regression guard for the
// production incident where Keycloak restarted and lost its realm, so the
// discovery endpoint returned 404 ("Realm does not exist"). Previously the
// portal API constructed its OIDC validator eagerly at boot and treated any
// error as fatal (os.Exit(1)), so the API crash-looped on every restart and
// ingress-nginx returned 503 for the entire — anonymous — catalog.
//
// With the lazy validator, New() must succeed despite an unreachable IdP, the
// anonymous catalog must serve normally, and a token-bearing request must get
// a retryable 503 (auth_unavailable) rather than crashing or a misleading 401.
func TestNew_OIDCUnreachableDoesNotCrash(t *testing.T) {
	// An IdP whose discovery endpoint 404s, exactly like a missing Keycloak
	// realm. Responds instantly so the startup warm-up fails fast.
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"Realm does not exist"}`, http.StatusNotFound)
	}))
	defer idp.Close()

	root := t.TempDir()
	testutil.BuildHubFixture(t, root, []testutil.HubComponentSpec{
		{Kind: "skill", Name: "checklist", Version: "1.0.0", Description: "review checklist", OwnerTeam: "dx-platform", Tags: []string{"review"}},
		{Kind: "agent", Name: "triage", Version: "0.1.0", Description: "triage agent", OwnerTeam: "dx-platform", Tags: []string{"ops"}},
	})
	t.Setenv("FDH_PORTAL_HUB_PATH", root)
	t.Setenv("FDH_PORTAL_REFRESH_INTERVAL", "60s")
	t.Setenv("OIDC_DISCOVERY_URL", idp.URL+"/realms/askenaz")
	t.Setenv("OIDC_CLIENT_ID", "fdh-portal")

	cfg, err := portalapi.LoadConfig()
	require.NoError(t, err)
	require.True(t, cfg.AuthEnabled(), "auth must be enabled for this test to be meaningful")

	// The crux: construction must NOT fail just because the IdP is down.
	srv, err := portalapi.New(cfg, portalapi.BuildInfo{Version: "test"})
	require.NoError(t, err, "New must tolerate an unreachable IdP at startup")

	require.NoError(t, srv.Refresh(context.Background()))
	h := srv.Handler()

	// Liveness + readiness come up despite the IdP being down.
	for _, path := range []string{"/healthz", "/readyz"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equalf(t, http.StatusOK, w.Code, "%s should be 200 with IdP down", path)
	}

	// Anonymous catalog reads work — this is what the public site depends on.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/components?limit=50", nil))
	require.Equal(t, http.StatusOK, w.Code, "anonymous catalog must serve while IdP is down")
	page := decode[map[string]any](t, w.Body)
	items, _ := page["items"].([]any)
	assert.GreaterOrEqual(t, len(items), 2, "catalog should list the fixture components")

	// A token-bearing request gets a retryable 503, not a crash and not a 401.
	wTok := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/components", nil)
	req.Header.Set("Authorization", "Bearer dummy.jwt.token")
	h.ServeHTTP(wTok, req)
	assert.Equal(t, http.StatusServiceUnavailable, wTok.Code, "token request should be 503 while IdP is unreachable")
	assert.NotEmpty(t, wTok.Header().Get("Retry-After"), "503 should advertise Retry-After")
}
