package portalapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/internal/portalapi"
	"github.com/forge/fdh/internal/testutil"
	"github.com/forge/fdh/pkg/bundle"
	"github.com/forge/fdh/pkg/registry"
)

// newWireTestServer wires a Server against the hub fixture without exercising
// the GitRegistry snapshot path. The HubPath points at internal/testutil/
// fixtures/hub/; the legacy RegistryLocalPath points at an empty tempdir so
// LoadConfig succeeds — the wire handlers never consult it.
//
// If hubPath is non-empty, it overrides the fixture path. Use "" for default.
func newWireTestServer(t *testing.T, hubPath string) http.Handler {
	t.Helper()
	if hubPath == "" {
		hubPath = testutil.HubFixturePath()
	}
	t.Setenv("FDH_PORTAL_REGISTRY_LOCAL_PATH", t.TempDir())
	t.Setenv("FDH_PORTAL_REFRESH_INTERVAL", "60s")
	t.Setenv("FDH_PORTAL_HUB_PATH", hubPath)

	cfg, err := portalapi.LoadConfig()
	require.NoError(t, err)
	srv, err := portalapi.New(cfg, portalapi.BuildInfo{Version: "test"})
	require.NoError(t, err)
	return srv.Handler()
}

// do issues an HTTP request through the handler under test.
func do(t *testing.T, h http.Handler, method, path string, hdr http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// --- /v1/index.json ---

func TestWireIndex_ReturnsSkillsOnly(t *testing.T) {
	h := newWireTestServer(t, "")
	w := do(t, h, http.MethodGet, "/v1/index.json", nil)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	require.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"))
	require.NotEmpty(t, w.Header().Get("ETag"))

	// Strict-decode through the same struct the CLI consumer uses.
	var idx registry.Index
	dec := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&idx))

	require.Equal(t, 1, idx.SchemaVersion)
	require.Equal(t, "forge-development-hub", idx.Registry)
	require.Len(t, idx.Skills, 1, "only skill components appear in /v1/index.json v1")
	require.Equal(t, "forge", idx.Skills[0].Namespace)
	require.Equal(t, "test-skill", idx.Skills[0].Name)
	require.Equal(t, "0.1.0", idx.Skills[0].LatestVersion)
	require.Len(t, idx.Skills[0].LatestHash, 64)
}

func TestWireIndex_HubNotReady(t *testing.T) {
	h := newWireTestServer(t, filepath.Join(t.TempDir(), "no-hub-here"))
	w := do(t, h, http.MethodGet, "/v1/index.json", nil)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Equal(t, "5", w.Header().Get("Retry-After"))

	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "hub_not_ready", body["error"])
}

func TestWireIndex_CacheControlHeader(t *testing.T) {
	h := newWireTestServer(t, "")
	w := do(t, h, http.MethodGet, "/v1/index.json", nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "public, max-age=60, must-revalidate", w.Header().Get("Cache-Control"))
}

// --- /v1/{kindPlural}/forge/{name}/manifest.json ---

func TestWireManifest_HappyPath_AllKinds(t *testing.T) {
	h := newWireTestServer(t, "")
	cases := []struct {
		plural, name string
	}{
		{"skills", "test-skill"},
		{"rules", "test-rule"},
		{"agents", "test-agent"},
		{"hooks", "test-hook"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.plural+"/"+tc.name, func(t *testing.T) {
			url := "/v1/" + tc.plural + "/forge/" + tc.name + "/manifest.json"
			w := do(t, h, http.MethodGet, url, nil)
			require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

			var m registry.Manifest
			dec := json.NewDecoder(w.Body)
			dec.DisallowUnknownFields()
			require.NoError(t, dec.Decode(&m))

			require.Equal(t, 1, m.SchemaVersion)
			require.Equal(t, "forge", m.Namespace)
			require.Equal(t, tc.name, m.Name)
			require.Equal(t, "0.1.0", m.Latest)
			require.Len(t, m.Versions, 1)
			require.Equal(t, "0.1.0", m.Versions[0].Version)
			require.Len(t, m.Versions[0].ContentHash, 64)
		})
	}
}

func TestWireManifest_NonForgeNamespace(t *testing.T) {
	h := newWireTestServer(t, "")
	w := do(t, h, http.MethodGet, "/v1/skills/other/test-skill/manifest.json", nil)
	require.Equal(t, http.StatusNotFound, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "not_found", body["error"])
	require.Equal(t, "other", body["namespace"])
	require.Equal(t, "test-skill", body["name"])
}

func TestWireManifest_MissingComponent(t *testing.T) {
	h := newWireTestServer(t, "")
	w := do(t, h, http.MethodGet, "/v1/skills/forge/does-not-exist/manifest.json", nil)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestWireManifest_UnknownKind(t *testing.T) {
	h := newWireTestServer(t, "")
	w := do(t, h, http.MethodGet, "/v1/whatever/forge/test-skill/manifest.json", nil)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestWireManifest_CacheControlHeader(t *testing.T) {
	h := newWireTestServer(t, "")
	w := do(t, h, http.MethodGet, "/v1/skills/forge/test-skill/manifest.json", nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "public, max-age=60, must-revalidate", w.Header().Get("Cache-Control"))
}

// --- /v1/{plural}/forge/{name}/versions/{v}/bundle.tar.gz ---

func TestWireBundleTarball_Determinism(t *testing.T) {
	h := newWireTestServer(t, "")
	url := "/v1/skills/forge/test-skill/versions/0.1.0/bundle.tar.gz"

	first := do(t, h, http.MethodGet, url, nil)
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, "application/gzip", first.Header().Get("Content-Type"))
	firstBody := first.Body.Bytes()
	firstETag := first.Header().Get("ETag")
	require.NotEmpty(t, firstETag)

	for i := 0; i < 9; i++ {
		w := do(t, h, http.MethodGet, url, nil)
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, firstETag, w.Header().Get("ETag"))
		require.True(t, bytes.Equal(firstBody, w.Body.Bytes()), "iteration %d bytes differ", i+1)
	}
}

func TestWireBundleTarball_MissingVersion(t *testing.T) {
	h := newWireTestServer(t, "")
	w := do(t, h, http.MethodGet, "/v1/skills/forge/test-skill/versions/9.9.9/bundle.tar.gz", nil)
	require.Equal(t, http.StatusNotFound, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "not_found", body["error"])
	require.Equal(t, "9.9.9", body["version"])
}

func TestWireBundleTarball_NonForgeNamespace(t *testing.T) {
	h := newWireTestServer(t, "")
	w := do(t, h, http.MethodGet, "/v1/skills/other/test-skill/versions/0.1.0/bundle.tar.gz", nil)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestWireBundleTarball_ImmutableCacheControl(t *testing.T) {
	h := newWireTestServer(t, "")
	w := do(t, h, http.MethodGet, "/v1/skills/forge/test-skill/versions/0.1.0/bundle.tar.gz", nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "public, max-age=31536000, immutable", w.Header().Get("Cache-Control"))
}

// --- /v1/{plural}/forge/{name}/versions/{v}/bundle.sha256 ---

func TestWireBundleSidecar_MatchesBundleLoadHash(t *testing.T) {
	h := newWireTestServer(t, "")
	w := do(t, h, http.MethodGet, "/v1/skills/forge/test-skill/versions/0.1.0/bundle.sha256", nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))

	parts := strings.Fields(w.Body.String())
	require.GreaterOrEqual(t, len(parts), 2)
	wireHash := parts[0]
	require.Equal(t, "bundle.tar.gz", parts[1])
	require.Len(t, wireHash, 64)

	// Direct comparison: the sidecar MUST equal bundle.HashDir over the source
	// directory — this is the invariant the CLI consumer relies on when
	// verifying FetchBundle (cf. HTTPRegistry.FetchBundle in pkg/registry/http.go).
	expected, err := bundle.HashDir(filepath.Join(testutil.HubFixturePath(), "skills", "test-skill"))
	require.NoError(t, err)
	require.Equal(t, expected, wireHash)
}

func TestWireBundleSidecar_MatchesManifestContentHash(t *testing.T) {
	h := newWireTestServer(t, "")
	manifestResp := do(t, h, http.MethodGet, "/v1/skills/forge/test-skill/manifest.json", nil)
	require.Equal(t, http.StatusOK, manifestResp.Code)
	sidecarResp := do(t, h, http.MethodGet, "/v1/skills/forge/test-skill/versions/0.1.0/bundle.sha256", nil)
	require.Equal(t, http.StatusOK, sidecarResp.Code)

	var m registry.Manifest
	require.NoError(t, json.Unmarshal(manifestResp.Body.Bytes(), &m))
	manifestHash := m.Versions[0].ContentHash

	sidecarHash := strings.Fields(sidecarResp.Body.String())[0]
	require.Equal(t, manifestHash, sidecarHash,
		"manifest content_hash and sidecar SHA must agree — they describe the same canonical hash")
}

func TestWireBundleSidecar_ImmutableCacheControl(t *testing.T) {
	h := newWireTestServer(t, "")
	w := do(t, h, http.MethodGet, "/v1/skills/forge/test-skill/versions/0.1.0/bundle.sha256", nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "public, max-age=31536000, immutable", w.Header().Get("Cache-Control"))
}

// --- Conditional revalidation ---

func TestWireIndex_IfNoneMatch_Matches304(t *testing.T) {
	h := newWireTestServer(t, "")
	first := do(t, h, http.MethodGet, "/v1/index.json", nil)
	require.Equal(t, http.StatusOK, first.Code)
	etag := first.Header().Get("ETag")
	require.NotEmpty(t, etag)

	second := do(t, h, http.MethodGet, "/v1/index.json", http.Header{
		"If-None-Match": []string{etag},
	})
	require.Equal(t, http.StatusNotModified, second.Code)
	require.Empty(t, second.Body.String())
	require.Equal(t, etag, second.Header().Get("ETag"))
	require.Equal(t, "public, max-age=60, must-revalidate", second.Header().Get("Cache-Control"))
}

func TestWireIndex_IfNoneMatch_Stale200(t *testing.T) {
	h := newWireTestServer(t, "")
	w := do(t, h, http.MethodGet, "/v1/index.json", http.Header{
		"If-None-Match": []string{`"stale-etag-value"`},
	})
	require.Equal(t, http.StatusOK, w.Code)
	require.NotEmpty(t, w.Body.String())
	require.NotEqual(t, `"stale-etag-value"`, w.Header().Get("ETag"))
}

func TestWireBundleTarball_IfNoneMatch_Matches304(t *testing.T) {
	h := newWireTestServer(t, "")
	url := "/v1/skills/forge/test-skill/versions/0.1.0/bundle.tar.gz"
	first := do(t, h, http.MethodGet, url, nil)
	require.Equal(t, http.StatusOK, first.Code)
	etag := first.Header().Get("ETag")
	require.NotEmpty(t, etag)

	second := do(t, h, http.MethodGet, url, http.Header{
		"If-None-Match": []string{etag},
	})
	require.Equal(t, http.StatusNotModified, second.Code)
}

// --- UI handlers unaffected ---

func TestWireEndpointsDoNotConsultSnapshot(t *testing.T) {
	// Construct a server WITHOUT calling Refresh — Snapshot is nil.
	t.Setenv("FDH_PORTAL_REGISTRY_LOCAL_PATH", t.TempDir())
	t.Setenv("FDH_PORTAL_REFRESH_INTERVAL", "60s")
	t.Setenv("FDH_PORTAL_HUB_PATH", testutil.HubFixturePath())

	cfg, err := portalapi.LoadConfig()
	require.NoError(t, err)
	srv, err := portalapi.New(cfg, portalapi.BuildInfo{Version: "test"})
	require.NoError(t, err)

	h := srv.Handler()

	// Wire endpoint succeeds even though Refresh has not run.
	wire := do(t, h, http.MethodGet, "/v1/index.json", nil)
	assert.Equal(t, http.StatusOK, wire.Code, "wire endpoint should not require snapshot")

	// UI endpoint returns 503 because the snapshot is empty.
	ui := do(t, h, http.MethodGet, "/api/v1/skills", nil)
	assert.Equal(t, http.StatusServiceUnavailable, ui.Code, "UI endpoint should require snapshot")

	_ = context.Background()
}

func TestUIHandlersStillRespondAfterWireRegistration(t *testing.T) {
	// Sanity check that registering the wire routes did not steal traffic
	// from /api/v1/*. Use the original test helper (which calls Refresh).
	_, h := newTestServerWithHub(t)

	w := do(t, h, http.MethodGet, "/api/v1/skills", nil)
	require.Equal(t, http.StatusOK, w.Code)

	body, err := io.ReadAll(w.Body)
	require.NoError(t, err)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(body, &resp))
	items, _ := resp["items"].([]any)
	require.NotNil(t, items)
}

// newTestServerWithHub mirrors newTestServer (which only sets up a registry)
// but also points HubPath at the wire fixture so the same Server handles
// both UI and wire endpoints. Used by tests that want refresh-driven UI
// data AND working wire endpoints.
func newTestServerWithHub(t *testing.T) (*portalapi.Server, http.Handler) {
	t.Helper()
	root := t.TempDir()
	testutil.BuildRegistry(t, root, []testutil.SkillSpec{
		{
			Namespace: "code-review", Name: "checklist", Version: "1.0.0",
			Description: "review checklist", OwnerTeam: "dx", Tags: []string{"review"},
			Files: map[string]string{"SKILL.md": testutil.FixtureSKILLMD("checklist", "review checklist")},
		},
	})
	t.Setenv("FDH_PORTAL_REGISTRY_LOCAL_PATH", root)
	t.Setenv("FDH_PORTAL_REFRESH_INTERVAL", "60s")
	t.Setenv("FDH_PORTAL_HUB_PATH", testutil.HubFixturePath())

	cfg, err := portalapi.LoadConfig()
	require.NoError(t, err)
	srv, err := portalapi.New(cfg, portalapi.BuildInfo{Version: "test"})
	require.NoError(t, err)
	require.NoError(t, srv.Refresh(context.Background()))
	return srv, srv.Handler()
}
