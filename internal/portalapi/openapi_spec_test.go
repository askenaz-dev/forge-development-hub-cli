package portalapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// openapiPaths returns the keys of the top-level `paths:` map in the
// embedded OpenAPI spec served at /openapi.yaml.
func openapiPaths(t *testing.T, h http.Handler) []string {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	require.Equal(t, http.StatusOK, w.Code)

	var spec struct {
		Paths map[string]any `yaml:"paths"`
	}
	require.NoError(t, yaml.Unmarshal(w.Body.Bytes(), &spec))

	out := make([]string, 0, len(spec.Paths))
	for k := range spec.Paths {
		out = append(out, k)
	}
	return out
}

func TestOpenAPISpec_HasWireEndpoints(t *testing.T) {
	h := newWireTestServer(t, "")
	paths := openapiPaths(t, h)
	required := []string{
		"/v1/index.json",
		"/v1/{kindPlural}/{namespace}/{name}/manifest.json",
		"/v1/{kindPlural}/{namespace}/{name}/versions/{version}/bundle.tar.gz",
		"/v1/{kindPlural}/{namespace}/{name}/versions/{version}/bundle.sha256",
	}
	for _, p := range required {
		require.Contains(t, paths, p, "OpenAPI spec missing wire path %q", p)
	}
}

func TestOpenAPISpec_HasComponentEndpoints(t *testing.T) {
	h := newWireTestServer(t, "")
	paths := openapiPaths(t, h)
	required := []string{
		"/components",
		"/components/{kind}/{namespace}/{name}",
		"/components/{kind}/{namespace}/{name}/versions/{version}",
		"/components/{kind}/{namespace}/{name}/versions/{version}/document",
	}
	for _, p := range required {
		require.Contains(t, paths, p, "OpenAPI spec missing component path %q", p)
	}
}

func TestOpenAPISpec_UIPathsUnchanged(t *testing.T) {
	h := newWireTestServer(t, "")
	paths := openapiPaths(t, h)
	preserved := []string{
		"/skills",
		"/skills/{namespace}/{name}",
		"/skills/{namespace}/{name}/versions/{version}",
		"/skills/{namespace}/{name}/versions/{version}/skill-md",
		"/auth/me",
		"/refresh",
	}
	for _, p := range preserved {
		require.Contains(t, paths, p, "UI path %q must be preserved", p)
	}
}

// TestOpenAPISpec_HasTelemetryIngest asserts the Stage-1 telemetry ingest path
// is declared (server-rooted under /api/v1, so it appears as "/telemetry") and
// references the closed TelemetryEvent schema.
func TestOpenAPISpec_HasTelemetryIngest(t *testing.T) {
	h := newWireTestServer(t, "")
	paths := openapiPaths(t, h)
	require.Contains(t, paths, "/telemetry", "OpenAPI spec must declare the telemetry ingest path")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, strings.Contains(w.Body.String(), "TelemetryEvent"),
		"OpenAPI spec must define the TelemetryEvent schema")
}

// TestOpenAPISpec_HasStage2AdminPaths asserts the Stage-2 admin analytics /
// observability / feedback / activity read paths are now declared (task 11.1),
// server-rooted under /api/v1 so they appear without the prefix, plus their
// aggregate schemas. Existing /v1 and /api/v1 paths remain (covered above).
func TestOpenAPISpec_HasStage2AdminPaths(t *testing.T) {
	h := newWireTestServer(t, "")
	paths := openapiPaths(t, h)
	required := []string{
		"/admin/analytics/summary",
		"/admin/analytics/top",
		"/admin/analytics/trends",
		"/admin/analytics/funnel",
		"/admin/observability",
		"/admin/feedback",
		"/admin/feedback/summary",
		"/admin/activity/claim",
		"/admin/activity",
	}
	for _, p := range required {
		require.Contains(t, paths, p, "OpenAPI spec missing Stage-2 admin path %q", p)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	for _, schema := range []string{
		"AnalyticsSummary", "AnalyticsTop", "AnalyticsTrends", "AnalyticsFunnel",
		"Observability", "FeedbackList", "FeedbackSummary", "ClaimRequest", "Activity",
		"StoreUnavailable",
	} {
		require.True(t, strings.Contains(body, schema),
			"OpenAPI spec must define the %q schema/response", schema)
	}
}

func TestOpenAPISpec_WireSchemasReferenced(t *testing.T) {
	h := newWireTestServer(t, "")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	for _, schema := range []string{
		"WireIndex",
		"WireIndexEntry",
		"WireManifest",
		"WireVersion",
		"WireError",
	} {
		require.True(t, strings.Contains(body, schema),
			"OpenAPI spec must define %q schema", schema)
	}
}
