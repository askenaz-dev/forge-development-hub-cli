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

// newScanStatusServer wires a portal over a hub fixture with one clean
// component and one carrying a blocking secret, so the real fdh-scan verdict
// (capability portal-scan-status) can be asserted end-to-end through both the
// wire protocol and the human /api/v1 endpoints.
func newScanStatusServer(t *testing.T) (http.Handler, string) {
	t.Helper()
	root := t.TempDir()
	testutil.BuildHubFixture(t, root, []testutil.HubComponentSpec{
		{Kind: "skill", Name: "clean-skill", Version: "1.0.0", Description: "clean", OwnerTeam: "platform"},
		{Kind: "skill", Name: "leaky-skill", Version: "1.0.0", Description: "leaky", OwnerTeam: "platform",
			Files: map[string]string{
				"creds.txt": "token: ghp_abcdefghijklmnopqrstuvwxyz1234567890\n",
			}},
	})
	t.Setenv("FDH_PORTAL_HUB_PATH", root)
	t.Setenv("FDH_PORTAL_REFRESH_INTERVAL", "60s")

	cfg, err := portalapi.LoadConfig()
	require.NoError(t, err)
	srv, err := portalapi.New(cfg, portalapi.BuildInfo{Version: "test"})
	require.NoError(t, err)
	require.NoError(t, srv.Refresh(context.Background()))
	return srv.Handler(), root
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// scanStatusByName indexes the {items|components} of a list/index body by name.
func scanStatusByName(t *testing.T, body map[string]any, key string) map[string]string {
	t.Helper()
	raw, ok := body[key].([]any)
	require.True(t, ok, "body[%q] is not a list: %v", key, body)
	out := map[string]string{}
	for _, it := range raw {
		m := it.(map[string]any)
		out[m["name"].(string)], _ = m["scan_status"].(string)
	}
	return out
}

// Task 2.4 — the wire /v1/index.json serves the real per-component verdict.
func TestScanStatus_WireIndexReal(t *testing.T) {
	h, _ := newScanStatusServer(t)
	w := get(t, h, "/v1/index.json")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	body := decode[map[string]any](t, w.Body)
	got := scanStatusByName(t, body, "components")
	assert.Equal(t, "pass", got["clean-skill"])
	assert.Equal(t, "fail", got["leaky-skill"])
}

// Task 2.4 — the wire manifest carries the verdict on the latest version.
func TestScanStatus_WireManifestReal(t *testing.T) {
	h, _ := newScanStatusServer(t)
	w := get(t, h, "/v1/skills/platform/leaky-skill/manifest.json")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	body := decode[map[string]any](t, w.Body)
	versions := body["versions"].([]any)
	require.NotEmpty(t, versions)
	latest := versions[0].(map[string]any)
	assert.Equal(t, "fail", latest["scan_status"])
}

// Task 2.4 — the human catalog reflects the real value, and the ?scan_status=
// filter (task 2.3) operates against it.
func TestScanStatus_ListAndFilter(t *testing.T) {
	h, _ := newScanStatusServer(t)

	all := decode[map[string]any](t, get(t, h, "/api/v1/components").Body)
	got := scanStatusByName(t, all, "items")
	assert.Equal(t, "pass", got["clean-skill"])
	assert.Equal(t, "fail", got["leaky-skill"])

	filtered := decode[map[string]any](t, get(t, h, "/api/v1/components?scan_status=fail").Body)
	names := scanStatusByName(t, filtered, "items")
	_, hasLeaky := names["leaky-skill"]
	_, hasClean := names["clean-skill"]
	assert.True(t, hasLeaky, "fail filter returns leaky-skill")
	assert.False(t, hasClean, "fail filter excludes clean-skill")
}

// Task 2.2 — the per-version /api/v1 endpoint serves the real verdict, not the
// removed sentinel.
func TestScanStatus_GetComponentVersion(t *testing.T) {
	h, _ := newScanStatusServer(t)
	w := get(t, h, "/api/v1/components/skill/platform/leaky-skill/versions/1.0.0")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	body := decode[map[string]any](t, w.Body)
	assert.Equal(t, "fail", body["scan_status"])
}
