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

// newComponentTestServer wires a server over a hub fixture publishing one
// component of every kind, so the kind-aware /api/v1/components endpoints can
// be exercised across skill, rule, agent, and hook.
func newComponentTestServer(t *testing.T) http.Handler {
	t.Helper()
	root := t.TempDir()
	testutil.BuildHubFixture(t, root, []testutil.HubComponentSpec{
		{Kind: "skill", Name: "design-system", Version: "0.5.0", Description: "design system", OwnerTeam: "dx-platform", Tags: []string{"ui"}},
		{Kind: "rule", Name: "no-console", Version: "0.2.0", Description: "no console logging", OwnerTeam: "dx-platform", Tags: []string{"quality"}},
		{Kind: "agent", Name: "pr-writer", Version: "0.1.0", Description: "writes PRs", OwnerTeam: "dx-platform", Tags: []string{"pr"}},
		{Kind: "hook", Name: "doctor-on-start", Version: "0.1.0", Description: "runs doctor", OwnerTeam: "dx-platform", Tags: []string{"lifecycle"}},
	})
	t.Setenv("FDH_PORTAL_HUB_PATH", root)
	t.Setenv("FDH_PORTAL_REFRESH_INTERVAL", "60s")

	cfg, err := portalapi.LoadConfig()
	require.NoError(t, err)
	srv, err := portalapi.New(cfg, portalapi.BuildInfo{Version: "test"})
	require.NoError(t, err)
	require.NoError(t, srv.Refresh(context.Background()))
	return srv.Handler()
}

func TestListComponents_SpansAllKinds(t *testing.T) {
	h := newComponentTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/components", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := decode[map[string]any](t, w.Body)
	items, _ := body["items"].([]any)
	require.Len(t, items, 4)
	kinds := map[string]bool{}
	for _, it := range items {
		m := it.(map[string]any)
		kinds[m["kind"].(string)] = true
		assert.Equal(t, "dx-platform", m["namespace"])
	}
	for _, k := range []string{"skill", "rule", "agent", "hook"} {
		assert.True(t, kinds[k], "kind %q present", k)
	}
}

func TestListComponents_FilterByKind(t *testing.T) {
	h := newComponentTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/components?kind=rule", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := decode[map[string]any](t, w.Body)
	items, _ := body["items"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, "rule", items[0].(map[string]any)["kind"])
	assert.Equal(t, "no-console", items[0].(map[string]any)["name"])
}

func TestListComponents_InvalidKind(t *testing.T) {
	h := newComponentTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/components?kind=template", nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetComponent_Agent(t *testing.T) {
	h := newComponentTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/components/agent/dx-platform/pr-writer", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := decode[map[string]any](t, w.Body)
	assert.Equal(t, "agent", body["kind"])
	assert.Equal(t, "pr-writer", body["name"])
	assert.Equal(t, "0.1.0", body["latest"])
}

func TestGetComponent_NotFound(t *testing.T) {
	h := newComponentTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/components/rule/dx-platform/missing", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
	body := decode[map[string]any](t, w.Body)
	assert.Equal(t, "component_not_found", body["error"])
	assert.Equal(t, "rule", body["kind"])
}

func TestGetComponent_InvalidKind(t *testing.T) {
	h := newComponentTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/components/template/forge/x", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetComponentVersion_Hook(t *testing.T) {
	h := newComponentTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/components/hook/dx-platform/doctor-on-start/versions/0.1.0", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := decode[map[string]any](t, w.Body)
	assert.Equal(t, "0.1.0", body["version"])
	assert.Contains(t, body["document_url"], "/versions/0.1.0/document")
}

func TestGetComponentDocument_Rule(t *testing.T) {
	h := newComponentTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/components/rule/dx-platform/no-console/versions/0.2.0/document", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/markdown; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), "name: no-console")
}

func TestSkillsEndpointEqualsComponentsKindSkill(t *testing.T) {
	h := newComponentTestServer(t)

	names := func(path string) map[string]bool {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, w.Code)
		body := decode[map[string]any](t, w.Body)
		items, _ := body["items"].([]any)
		out := map[string]bool{}
		for _, it := range items {
			out[it.(map[string]any)["name"].(string)] = true
		}
		return out
	}

	assert.Equal(t, names("/api/v1/skills"), names("/api/v1/components?kind=skill"),
		"the skills endpoint must return the same items as components filtered to kind=skill")
}
