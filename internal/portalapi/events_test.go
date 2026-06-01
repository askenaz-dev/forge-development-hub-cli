package portalapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func postEvent(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, r)
	return w
}

func TestPostEvents_AcceptsKnownEvent(t *testing.T) {
	_, h := newTestServer(t)
	w := postEvent(t, h, `{"event_name":"component.installed","attributes":{"kind":"skill","name":"owasp"}}`)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, decode[map[string]any](t, w.Body)["recorded"].(bool))
}

func TestPostEvents_RejectsUnknownEvent(t *testing.T) {
	_, h := newTestServer(t)
	w := postEvent(t, h, `{"event_name":"surveil.session"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPostEvents_RequiresEventName(t *testing.T) {
	_, h := newTestServer(t)
	w := postEvent(t, h, `{"attributes":{"kind":"skill"}}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPostEvents_AnonymousAllowed(t *testing.T) {
	_, h := newTestServer(t)
	// No Authorization header — onboarding/CLI submit pre-auth.
	w := postEvent(t, h, `{"event_name":"feedback.submitted","attributes":{"sentiment":"up","name":"owasp"}}`)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestInsights_ForbiddenForAnonymous(t *testing.T) {
	_, h := newTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/insights", nil))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestBundleDownload_IncrementsDownloadMetric(t *testing.T) {
	_, h := newTestServer(t)
	// Download a real fixture bundle (skill checklist under dx-platform).
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/skills/dx-platform/checklist/versions/1.0.0/bundle.tar.gz", nil)
	h.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	// The Prometheus counter must now report the download.
	mw := httptest.NewRecorder()
	h.ServeHTTP(mw, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, mw.Code)
	assert.Contains(t, mw.Body.String(), "fdh_portal_api_bundle_download_total")
}
