package portalapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/internal/portalapi"
	"github.com/forge/fdh/internal/testutil"
)

// newTestServer wires a Server whose human API serves the real hub catalog
// (FDH_PORTAL_HUB_PATH) — the same source the wire endpoints use. The hub
// fixture publishes two skills (under the "dx-platform" namespace sentinel) plus a
// rule, so the skills endpoints stay skill-scoped while exercising real data.
func newTestServer(t *testing.T) (*portalapi.Server, http.Handler) {
	t.Helper()
	root := t.TempDir()
	testutil.BuildHubFixture(t, root, []testutil.HubComponentSpec{
		{Kind: "skill", Name: "checklist", Version: "1.0.0", Description: "review checklist", OwnerTeam: "dx-platform", Tags: []string{"review"}},
		{Kind: "skill", Name: "owasp", Version: "1.2.0", Description: "owasp sweep", OwnerTeam: "dx-platform", Tags: []string{"owasp", "security"}},
		{Kind: "rule", Name: "no-console", Version: "0.2.0", Description: "no console logging", OwnerTeam: "dx-platform", Tags: []string{"quality"}},
	})
	t.Setenv("FDH_PORTAL_HUB_PATH", root)
	t.Setenv("FDH_PORTAL_REFRESH_INTERVAL", "60s")

	cfg, err := portalapi.LoadConfig()
	require.NoError(t, err)
	srv, err := portalapi.New(cfg, portalapi.BuildInfo{Version: "test"})
	require.NoError(t, err)

	// Initial refresh — synchronous so handlers see data.
	require.NoError(t, srv.Refresh(context.Background()))
	return srv, srv.Handler()
}

func decode[T any](t *testing.T, body io.Reader) T {
	t.Helper()
	var v T
	require.NoError(t, json.NewDecoder(body).Decode(&v))
	return v
}

func TestHealthz(t *testing.T) {
	_, h := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestReadyz_ReadyAfterRefresh(t *testing.T) {
	_, h := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestListSkills_AllItems(t *testing.T) {
	_, h := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil))
	require.Equal(t, http.StatusOK, w.Code)

	body := decode[map[string]any](t, w.Body)
	items, _ := body["items"].([]any)
	assert.Len(t, items, 2, "only the two skill components, not the rule")
}

func TestListSkills_FilterByQuery(t *testing.T) {
	_, h := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills?q=owasp", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := decode[map[string]any](t, w.Body)
	items, _ := body["items"].([]any)
	require.Len(t, items, 1)
	first := items[0].(map[string]any)
	assert.Equal(t, "owasp", first["name"])
}

func TestListSkills_FilterByNamespace(t *testing.T) {
	_, h := newTestServer(t)
	w := httptest.NewRecorder()
	// Hub components share the "dx-platform" namespace sentinel.
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills?namespace=dx-platform", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := decode[map[string]any](t, w.Body)
	items, _ := body["items"].([]any)
	require.Len(t, items, 2)
	first := items[0].(map[string]any)
	assert.Equal(t, "dx-platform", first["namespace"])
}

func TestListSkills_Pagination(t *testing.T) {
	_, h := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills?limit=1", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := decode[map[string]any](t, w.Body)
	items, _ := body["items"].([]any)
	assert.Len(t, items, 1)
	assert.NotNil(t, body["next_cursor"])
	cursor, _ := body["next_cursor"].(string)
	require.NotEmpty(t, cursor)

	// Second page.
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/api/v1/skills?limit=1&cursor="+cursor, nil))
	body2 := decode[map[string]any](t, w2.Body)
	items2, _ := body2["items"].([]any)
	assert.Len(t, items2, 1)
	first1 := items[0].(map[string]any)
	first2 := items2[0].(map[string]any)
	assert.NotEqual(t, first1["name"], first2["name"], "second page must differ from first")
}

func TestGetSkill(t *testing.T) {
	_, h := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills/dx-platform/owasp", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := decode[map[string]any](t, w.Body)
	assert.Equal(t, "owasp", body["name"])
	assert.Equal(t, "dx-platform", body["namespace"])
	assert.Equal(t, "1.2.0", body["latest"])
	versions, _ := body["versions"].([]any)
	require.Len(t, versions, 1)
}

func TestGetSkill_NotFound(t *testing.T) {
	_, h := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills/dx-platform/no-such", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetSkill_WrongNamespace(t *testing.T) {
	_, h := newTestServer(t)
	w := httptest.NewRecorder()
	// A non-forge namespace never resolves under the sentinel scheme.
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills/security/owasp", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetSkillVersion(t *testing.T) {
	_, h := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills/dx-platform/owasp/versions/1.2.0", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := decode[map[string]any](t, w.Body)
	assert.Equal(t, "1.2.0", body["version"])
	assert.Contains(t, body["skill_md_url"], "/versions/1.2.0/skill-md")
}

func TestGetSkillMarkdown(t *testing.T) {
	_, h := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills/dx-platform/owasp/versions/1.2.0/skill-md", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/markdown; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), "name: owasp")
}

func TestAuthMe_Anonymous(t *testing.T) {
	_, h := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := decode[map[string]any](t, w.Body)
	assert.Equal(t, "anonymous", body["role"])
	_, hasSub := body["sub"]
	assert.False(t, hasSub, "anonymous user must not include sub")
}

func TestRefresh_NoAuthAllowed(t *testing.T) {
	// When OIDC is not configured, refresh is permitted (dev convenience).
	_, h := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/refresh", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := decode[map[string]any](t, w.Body)
	assert.Contains(t, body, "refreshed_at")
	assert.Equal(t, float64(2), body["skill_count"])
	assert.Equal(t, float64(3), body["component_count"])
}

func TestOpenAPISpec_Embedded(t *testing.T) {
	_, h := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/yaml; charset=utf-8", w.Header().Get("Content-Type"))
	assert.True(t, strings.Contains(w.Body.String(), "openapi: 3.1.0"))
}

func TestRefreshLoop_PicksUpChanges(t *testing.T) {
	// Build a hub fixture, then add a third skill to the same root and confirm
	// a refresh picks it up.
	root := t.TempDir()
	base := []testutil.HubComponentSpec{
		{Kind: "skill", Name: "checklist", Version: "1.0.0", Description: "review checklist", OwnerTeam: "dx-platform"},
		{Kind: "skill", Name: "owasp", Version: "1.2.0", Description: "owasp sweep", OwnerTeam: "dx-platform"},
	}
	testutil.BuildHubFixture(t, root, base)
	t.Setenv("FDH_PORTAL_HUB_PATH", root)
	t.Setenv("FDH_PORTAL_REFRESH_INTERVAL", "60s")

	cfg, err := portalapi.LoadConfig()
	require.NoError(t, err)
	srv, err := portalapi.New(cfg, portalapi.BuildInfo{Version: "test"})
	require.NoError(t, err)
	require.NoError(t, srv.Refresh(context.Background()))
	h := srv.Handler()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil))
	items, _ := decode[map[string]any](t, w.Body)["items"].([]any)
	require.Len(t, items, 2)

	// Add a third skill and refresh.
	testutil.BuildHubFixture(t, root, append(base,
		testutil.HubComponentSpec{Kind: "skill", Name: "unit", Version: "1.0.0", Description: "unit test generation", OwnerTeam: "dx-platform"},
	))
	require.NoError(t, srv.Refresh(context.Background()))

	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil))
	items2, _ := decode[map[string]any](t, w2.Body)["items"].([]any)
	assert.Len(t, items2, 3, "third skill should appear after refresh")
}
