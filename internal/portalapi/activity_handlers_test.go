package portalapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/internal/portalapi/auth"
	"github.com/forge/fdh/internal/portalapi/telemetry"
)

// claimReq builds a POST /api/v1/admin/activity/claim request carrying the
// admin principal (the BFF service credential / a human admin), with the given
// JSON body.
func claimReq(body string, u auth.User) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/admin/activity/claim", strings.NewReader(body))
	return r.WithContext(requestAs("/api/v1/admin/activity/claim", u).Context())
}

// TestClaim_NonAdmin_403 proves the claim endpoint is admin-gated.
func TestClaim_NonAdmin_403(t *testing.T) {
	s := adminStatsServer(newFakeStore())
	w := httptest.NewRecorder()
	s.handlePostActivityClaim(w, claimReq(`{"install_id":"abc","user":"dev@example.com"}`, auth.Anonymous()))
	require.Equal(t, http.StatusForbidden, w.Code)
}

// TestClaim_MissingFields_400 proves both fields are required.
func TestClaim_MissingFields_400(t *testing.T) {
	s := adminStatsServer(newFakeStore())
	w := httptest.NewRecorder()
	s.handlePostActivityClaim(w, claimReq(`{"install_id":""}`, auth.User{Role: auth.RoleAdmin}))
	require.Equal(t, http.StatusBadRequest, w.Code)
}

// TestClaim_StoreOutage_503 proves a claim during a store outage degrades to the
// typed store_unavailable (a claim is an explicit action whose success matters,
// unlike best-effort ingest).
func TestClaim_StoreOutage_503(t *testing.T) {
	fs := newFakeStore()
	fs.degraded = true
	s := adminStatsServer(fs)
	w := httptest.NewRecorder()
	s.handlePostActivityClaim(w, claimReq(`{"install_id":"abc","user":"dev@example.com"}`, auth.User{Role: auth.RoleAdmin}))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.NotEmpty(t, w.Header().Get("Retry-After"))
}

// TestClaim_ThenActivity_RoundTrip is task 8.2 / D5: an explicit claim links a
// machine's install id to the user, after which that machine's install events
// appear in the user's activity feed — and ONLY then.
func TestClaim_ThenActivity_RoundTrip(t *testing.T) {
	fs := newFakeStore()
	// One install event under a pseudonymous id, no PII.
	fs.events = []telemetry.Event{
		{Event: "install", Kind: "skill", Name: "design-system", Version: "1.2.0", InstallID: "machine-xyz"},
	}
	s := adminStatsServer(fs)
	admin := auth.User{Role: auth.RoleAdmin}

	// Before any claim, the user's activity feed is empty (no de-pseudonymization).
	bw := httptest.NewRecorder()
	s.handleGetActivity(bw, requestAs("/api/v1/admin/activity?user=dev@example.com", admin))
	require.Equal(t, http.StatusOK, bw.Code)
	var before struct {
		Installs []map[string]any `json:"installs"`
	}
	require.NoError(t, json.Unmarshal(bw.Body.Bytes(), &before))
	assert.Empty(t, before.Installs, "no install activity appears before an explicit claim")

	// The user claims this machine's install id.
	cw := httptest.NewRecorder()
	s.handlePostActivityClaim(cw, claimReq(`{"install_id":"machine-xyz","user":"dev@example.com"}`, admin))
	require.Equal(t, http.StatusAccepted, cw.Code, "body=%s", cw.Body.String())

	// Now the install appears in the feed.
	aw := httptest.NewRecorder()
	s.handleGetActivity(aw, requestAs("/api/v1/admin/activity?user=dev@example.com", admin))
	require.Equal(t, http.StatusOK, aw.Code)
	var after struct {
		Installs []struct {
			Kind    string `json:"kind"`
			Name    string `json:"name"`
			Version string `json:"version"`
			TS      string `json:"ts"`
		} `json:"installs"`
	}
	require.NoError(t, json.Unmarshal(aw.Body.Bytes(), &after))
	require.Len(t, after.Installs, 1)
	assert.Equal(t, "design-system", after.Installs[0].Name)
	assert.Equal(t, "1.2.0", after.Installs[0].Version)
}

// TestActivity_EmptyUser_OK proves an empty user yields an empty list, not an
// error (the empty-state contract, mirroring contributions).
func TestActivity_EmptyUser_OK(t *testing.T) {
	s := adminStatsServer(newFakeStore())
	w := httptest.NewRecorder()
	s.handleGetActivity(w, requestAs("/api/v1/admin/activity", auth.User{Role: auth.RoleAdmin}))
	require.Equal(t, http.StatusOK, w.Code)
	var env struct {
		Installs []any `json:"installs"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.Empty(t, env.Installs)
}

// TestClaim_IsTheOnlyIdentityLink is task 12.2: the platform stores an
// install_id↔identity mapping ONLY via the explicit user claim. We assert (a)
// the events the store holds never carry an identity field after an unclaimed
// ingest+activation pass, and (b) the claim lands in the SEPARATE claims store,
// never merged into the events list.
func TestClaim_IsTheOnlyIdentityLink(t *testing.T) {
	fs := newFakeStore()
	s := adminStatsServer(fs)
	admin := auth.User{Role: auth.RoleAdmin}

	// Drive an anonymous telemetry ingest + an activation, neither of which may
	// record any identity.
	iw := httptest.NewRecorder()
	s.handlePostTelemetry(iw, httptest.NewRequest(http.MethodPost, "/api/v1/telemetry",
		strings.NewReader(`{"event":"install","kind":"skill","name":"ds","install_id":"machine-1","os":"linux","locale":"en"}`)))
	require.Equal(t, http.StatusAccepted, iw.Code)

	aw := httptest.NewRecorder()
	s.handlePostActivation(aw, httptest.NewRequest(http.MethodPost, "/api/v1/activation",
		strings.NewReader(`{"step":"done","wizard_session_id":"s1","locale":"en","os":"linux"}`)))
	require.Equal(t, http.StatusOK, aw.Code)

	// Every stored event row carries NO identity (no email/user/name); only the
	// pseudonymous install_id, which is not an identity.
	fs.mu.Lock()
	for _, e := range fs.events {
		assert.Empty(t, e.Category == "email" || e.Category == "user", "no identity smuggled into a row")
	}
	claimsBefore := len(fs.claims)
	fs.mu.Unlock()
	assert.Equal(t, 0, claimsBefore, "no identity↔telemetry link exists before an explicit claim")

	// The ONLY way a link is created is the explicit claim.
	cw := httptest.NewRecorder()
	s.handlePostActivityClaim(cw, claimReq(`{"install_id":"machine-1","user":"dev@example.com"}`, admin))
	require.Equal(t, http.StatusAccepted, cw.Code)

	fs.mu.Lock()
	defer fs.mu.Unlock()
	require.Len(t, fs.claims, 1, "the explicit claim is the single identity link")
	assert.Equal(t, "machine-1", fs.claims[0].installID)
	assert.Equal(t, "dev@example.com", fs.claims[0].user)
	// The claim is stored SEPARATELY: it never appears as an events row, so the
	// PII-free events table is never joined to identity except via the claim.
	for _, e := range fs.events {
		assert.NotEqual(t, "dev@example.com", e.Text, "the claimed email must not leak into an events row")
	}
}
