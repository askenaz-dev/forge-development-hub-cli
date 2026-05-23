package portalapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/falabella/fdh/internal/portalapi"
	"github.com/falabella/fdh/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestServer(t *testing.T) (*portalapi.Server, http.Handler) {
	t.Helper()
	root := t.TempDir()
	testutil.BuildRegistry(t, root, []testutil.SkillSpec{
		{
			Namespace: "code-review", Name: "checklist", Version: "1.0.0",
			Description: "review checklist", OwnerTeam: "dx", Tags: []string{"review"},
			Files: map[string]string{"SKILL.md": testutil.FixtureSKILLMD("checklist", "review checklist")},
		},
		{
			Namespace: "security", Name: "owasp", Version: "1.2.0",
			Description: "owasp sweep", OwnerTeam: "appsec", Tags: []string{"owasp", "security"},
			Files: map[string]string{"SKILL.md": testutil.FixtureSKILLMD("owasp", "owasp sweep")},
		},
	})
	t.Setenv("FDH_PORTAL_REGISTRY_LOCAL_PATH", root)
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
	assert.Len(t, items, 2)
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
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills?namespace=security", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := decode[map[string]any](t, w.Body)
	items, _ := body["items"].([]any)
	require.Len(t, items, 1)
	first := items[0].(map[string]any)
	assert.Equal(t, "security", first["namespace"])
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
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills/security/owasp", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := decode[map[string]any](t, w.Body)
	assert.Equal(t, "owasp", body["name"])
	assert.Equal(t, "1.2.0", body["latest"])
	versions, _ := body["versions"].([]any)
	require.Len(t, versions, 1)
}

func TestGetSkill_NotFound(t *testing.T) {
	_, h := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills/no/such", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetSkillVersion(t *testing.T) {
	_, h := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills/security/owasp/versions/1.2.0", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := decode[map[string]any](t, w.Body)
	assert.Equal(t, "1.2.0", body["version"])
	assert.Contains(t, body["skill_md_url"], "/versions/1.2.0/skill-md")
}

func TestGetSkillMarkdown(t *testing.T) {
	_, h := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills/security/owasp/versions/1.2.0/skill-md", nil))
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
	// Build a fresh registry and a server pointed at it. Mutate the
	// registry on disk, call Refresh, and confirm the listing changes.
	srv, h := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil))
	body := decode[map[string]any](t, w.Body)
	items, _ := body["items"].([]any)
	require.Len(t, items, 2)

	// Add a third skill to the same registry root.
	root := getRegistryRoot(srv)
	testutil.BuildRegistry(t, root, []testutil.SkillSpec{
		{
			Namespace: "code-review", Name: "checklist", Version: "1.0.0",
			Description: "review checklist", OwnerTeam: "dx",
			Files: map[string]string{"SKILL.md": testutil.FixtureSKILLMD("checklist", "review checklist")},
		},
		{
			Namespace: "security", Name: "owasp", Version: "1.2.0",
			Description: "owasp sweep", OwnerTeam: "appsec",
			Files: map[string]string{"SKILL.md": testutil.FixtureSKILLMD("owasp", "owasp sweep")},
		},
		{
			Namespace: "testing", Name: "unit", Version: "1.0.0",
			Description: "unit test generation", OwnerTeam: "qa",
			Files: map[string]string{"SKILL.md": testutil.FixtureSKILLMD("unit", "unit test generation")},
		},
	})
	require.NoError(t, srv.Refresh(context.Background()))

	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil))
	body2 := decode[map[string]any](t, w2.Body)
	items2, _ := body2["items"].([]any)
	assert.Len(t, items2, 3, "third skill should appear after refresh")
}

// getRegistryRoot returns the registry path the test wired via t.Setenv.
func getRegistryRoot(srv *portalapi.Server) string {
	_ = srv
	return os.Getenv("FDH_PORTAL_REGISTRY_LOCAL_PATH")
}
