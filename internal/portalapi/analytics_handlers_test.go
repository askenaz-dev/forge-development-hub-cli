package portalapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/internal/portalapi/auth"
	"github.com/forge/fdh/internal/portalapi/telemetry"
)

// Stage-2 admin gating + degradation tests (task 11.3). Each new admin read is
// gated EXACTLY like handleGetActivation: 403 for a non-admin (anonymous /
// consumer), 200 for an admin (a human admin OR the BFF service credential
// mapped to admin — both arrive as role=admin on the request context). A
// degraded store yields the typed store_unavailable with Retry-After, never a
// 500. Aggregate payloads never carry an identity field.

// adminStatsServer returns a *Server wired with the given store and the obs
// stats + metrics needed by the observability handler.
func adminStatsServer(store telemetry.Store) *Server {
	s := &Server{telemetry: store, obs: newObsStats(), metrics: newMetrics()}
	s.logger = slogDiscard()
	return s
}

// ratingPtr is a small helper for feedback events.
func ratingPtr(n int) *int { return &n }

// adminReadCases enumerates every Stage-2 admin GET read as a name→invoker so
// the gating + degradation assertions run uniformly over all of them.
func adminReadCases() map[string]func(*Server, *http.Request) http.HandlerFunc {
	return map[string]func(*Server, *http.Request) http.HandlerFunc{
		"analytics/summary": func(s *Server, _ *http.Request) http.HandlerFunc { return s.handleGetAnalyticsSummary },
		"analytics/top":     func(s *Server, _ *http.Request) http.HandlerFunc { return s.handleGetAnalyticsTop },
		"analytics/trends":  func(s *Server, _ *http.Request) http.HandlerFunc { return s.handleGetAnalyticsTrends },
		"analytics/funnel":  func(s *Server, _ *http.Request) http.HandlerFunc { return s.handleGetAnalyticsFunnel },
		"feedback":          func(s *Server, _ *http.Request) http.HandlerFunc { return s.handleGetFeedback },
		"activity":          func(s *Server, _ *http.Request) http.HandlerFunc { return s.handleGetActivity },
	}
}

// TestAdminReads_Forbidden_NonAdmin proves every Stage-2 admin read rejects an
// anonymous AND a consumer principal with 403 (never reaching the store).
func TestAdminReads_Forbidden_NonAdmin(t *testing.T) {
	s := adminStatsServer(newFakeStore())
	principals := map[string]auth.User{
		"anonymous": auth.Anonymous(),
		"consumer":  {Role: auth.RoleConsumer, Email: "dev@example.com"},
	}
	for name, invoke := range adminReadCases() {
		for pname, principal := range principals {
			t.Run(name+"/"+pname, func(t *testing.T) {
				w := httptest.NewRecorder()
				invoke(s, nil)(w, requestAs("/api/v1/admin/"+name+"?user=dev@example.com&metric=install&event=install", principal))
				require.Equal(t, http.StatusForbidden, w.Code, "body=%s", w.Body.String())
				var env map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
				assert.Equal(t, "forbidden", env["error"])
			})
		}
	}
}

// TestAdminReads_OK_Admin proves every Stage-2 admin read returns 200 for an
// admin principal (and for the BFF service credential, which also arrives as
// role=admin via the fdh-portal-svc role-map).
func TestAdminReads_OK_Admin(t *testing.T) {
	s := adminStatsServer(newFakeStore())
	admin := auth.User{Role: auth.RoleAdmin, Sub: "admin1", Email: "admin@example.com"}
	for name, invoke := range adminReadCases() {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			invoke(s, nil)(w, requestAs("/api/v1/admin/"+name+"?user=admin@example.com&metric=install&event=install", admin))
			require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		})
	}
}

// TestAdminReads_StoreOutage_503 proves every read that touches the store
// degrades to the typed store_unavailable + Retry-After (not a 500) when the
// store is down. (observability is deliberately excluded — it must still render
// site health with the store down, see its own test.)
func TestAdminReads_StoreOutage_503(t *testing.T) {
	fs := newFakeStore()
	fs.degraded = true
	s := adminStatsServer(fs)
	admin := auth.User{Role: auth.RoleAdmin, Sub: "admin1"}

	for name, invoke := range adminReadCases() {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			invoke(s, nil)(w, requestAs("/api/v1/admin/"+name+"?user=admin@example.com&metric=install&event=install", admin))
			require.Equal(t, http.StatusServiceUnavailable, w.Code, "body=%s", w.Body.String())
			assert.NotEmpty(t, w.Header().Get("Retry-After"))
			var env map[string]string
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
			assert.Equal(t, "store_unavailable", env["error"])
		})
	}
}

// TestAnalyticsSummary_AggregatesNoIdentity proves the summary returns aggregate
// counts and a closed by_event breakdown with NO identity field (the privacy
// invariant: no analytics payload maps an install_id to an identity).
func TestAnalyticsSummary_AggregatesNoIdentity(t *testing.T) {
	fs := newFakeStore()
	// Seed events directly (bypassing the wire decode) — two installs + feedback.
	id := "deadbeef"
	fs.events = []telemetry.Event{
		{Event: "install", Kind: "skill", Namespace: "forge", Name: "ds", InstallID: id},
		{Event: "install", Kind: "skill", Namespace: "forge", Name: "ds", InstallID: id},
		{Event: "feedback", Rating: ratingPtr(5), Text: "great"},
	}
	s := adminStatsServer(fs)

	w := httptest.NewRecorder()
	s.handleGetAnalyticsSummary(w, requestAs("/api/v1/admin/analytics/summary",
		auth.User{Role: auth.RoleAdmin}))
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// The raw JSON must not contain any identity field name or the install_id
	// value — analytics is aggregates only.
	raw := w.Body.String()
	for _, forbidden := range []string{"install_id", "email", "user", "deadbeef"} {
		assert.NotContains(t, raw, forbidden, "analytics summary must not expose %q", forbidden)
	}

	var env struct {
		Total   int64            `json:"total"`
		ByEvent map[string]int64 `json:"by_event"`
		Since   string           `json:"since"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.Equal(t, int64(3), env.Total)
	assert.Equal(t, int64(2), env.ByEvent["install"])
	assert.Equal(t, int64(1), env.ByEvent["feedback"])
	// Closed breakdown is fully populated (zero for absent buckets).
	assert.Contains(t, env.ByEvent, "download")
	assert.Contains(t, env.ByEvent, "resolve")
	assert.Contains(t, env.ByEvent, "activation")
}

// TestAnalyticsTop_AggregateShape proves the top endpoint returns aggregate
// {kind,namespace,name,count} items and echoes the metric.
func TestAnalyticsTop_AggregateShape(t *testing.T) {
	fs := newFakeStore()
	fs.events = []telemetry.Event{
		{Event: "install", Kind: "skill", Namespace: "forge", Name: "ds"},
		{Event: "install", Kind: "skill", Namespace: "forge", Name: "ds"},
		{Event: "install", Kind: "rule", Namespace: "forge", Name: "lint"},
	}
	s := adminStatsServer(fs)

	w := httptest.NewRecorder()
	s.handleGetAnalyticsTop(w, requestAs("/api/v1/admin/analytics/top?metric=install&limit=5",
		auth.User{Role: auth.RoleAdmin}))
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var env struct {
		Metric string `json:"metric"`
		Items  []struct {
			Kind, Namespace, Name string
			Count                 int64
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.Equal(t, "install", env.Metric)
	require.Len(t, env.Items, 2)
}

// TestObservability_RendersWithStoreDown proves the observability surface still
// returns 200 with first-party site health when the store is unavailable (it
// must not 503 — design D7: render without an external query source). The store
// block reports available:false.
func TestObservability_RendersWithStoreDown(t *testing.T) {
	fs := newFakeStore()
	fs.available = false
	fs.degraded = true
	s := adminStatsServer(fs)
	// Simulate some served traffic so latency/error stats exist.
	s.obs.record(200, 5)
	s.obs.record(500, 12)

	w := httptest.NewRecorder()
	s.handleGetObservability(w, requestAs("/api/v1/admin/observability",
		auth.User{Role: auth.RoleAdmin}))
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var env struct {
		RequestsTotal uint64  `json:"requests_total"`
		ErrorRate     float64 `json:"error_rate"`
		Store         struct {
			Available  bool  `json:"available"`
			EventCount int64 `json:"event_count"`
		} `json:"store"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.Equal(t, uint64(2), env.RequestsTotal)
	assert.InDelta(t, 0.5, env.ErrorRate, 0.0001)
	assert.False(t, env.Store.Available, "store block reports degraded")
}

// TestObservability_NonAdmin_403 proves the observability gate.
func TestObservability_NonAdmin_403(t *testing.T) {
	s := adminStatsServer(newFakeStore())
	w := httptest.NewRecorder()
	s.handleGetObservability(w, requestAs("/api/v1/admin/observability", auth.Anonymous()))
	require.Equal(t, http.StatusForbidden, w.Code)
}

// TestFeedbackSummary_DisabledByDefault proves the summary is enabled:false with
// no flag/provider — no LLM dependency (design D8).
func TestFeedbackSummary_DisabledByDefault(t *testing.T) {
	s := adminStatsServer(newFakeStore())
	w := httptest.NewRecorder()
	s.handleGetFeedbackSummary(w, requestAs("/api/v1/admin/feedback/summary",
		auth.User{Role: auth.RoleAdmin}))
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var env struct {
		Enabled bool `json:"enabled"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.False(t, env.Enabled, "summary disabled without flag+provider (no LLM dependency)")
}

// TestFeedbackSummary_ActiveWhenFlagAndProvider proves the active branch when
// both the flag and a provider are set — still no real LLM call required.
func TestFeedbackSummary_ActiveWhenFlagAndProvider(t *testing.T) {
	s := adminStatsServer(newFakeStore())
	s.cfg.FeedbackSummaryEnabled = true
	s.cfg.FeedbackSummaryProvider = "claude"
	w := httptest.NewRecorder()
	s.handleGetFeedbackSummary(w, requestAs("/api/v1/admin/feedback/summary",
		auth.User{Role: auth.RoleAdmin}))
	require.Equal(t, http.StatusOK, w.Code)
	var env struct {
		Enabled bool `json:"enabled"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.True(t, env.Enabled)
}

// TestFeedbackList_NonAdmin_403 covers the feedback list gate explicitly.
func TestFeedbackList_NonAdmin_403(t *testing.T) {
	s := adminStatsServer(newFakeStore())
	w := httptest.NewRecorder()
	s.handleGetFeedback(w, requestAs("/api/v1/admin/feedback", auth.User{Role: auth.RoleConsumer}))
	require.Equal(t, http.StatusForbidden, w.Code)
}

// TestServiceCredential_AdminOnReads_AnonymousOnIngest is task 9.2: the BFF
// portal service credential — which arrives as role=admin via the
// fdh-portal-svc role-map entry — is accepted on an admin read, while the
// anonymous ingest endpoint never inspects auth, so presenting the same
// credential there confers NO privilege and the event is handled anonymously.
func TestServiceCredential_AdminOnReads_AnonymousOnIngest(t *testing.T) {
	fs := newFakeStore()
	s := &Server{
		telemetry:     fs,
		obs:           newObsStats(),
		metrics:       newMetrics(),
		ingestLimiter: newIngestLimiter(time.Minute, 1000),
	}
	s.logger = slogDiscard()

	// The service credential as the API sees it: a principal whose role-map
	// resolved to admin (sub identifies the service account, not a human).
	svc := auth.User{Role: auth.RoleAdmin, Sub: "service-account-fdh-portal-svc"}

	// Accepted as admin-equivalent on a read.
	rw := httptest.NewRecorder()
	s.handleGetAnalyticsFunnel(rw, requestAs("/api/v1/admin/analytics/funnel", svc))
	require.Equal(t, http.StatusOK, rw.Code, "service credential must authorize admin reads")

	// On ingest, auth is never read: the same credential confers no privilege,
	// the event is accepted anonymously and persisted (no identity recorded).
	iw := httptest.NewRecorder()
	ir := httptest.NewRequest(http.MethodPost, "/api/v1/telemetry",
		strings.NewReader(`{"event":"install","kind":"skill","name":"ds","os":"linux","locale":"en"}`))
	// Even if a token were attached, handlePostTelemetry never inspects it.
	ir = ir.WithContext(requestAs("/api/v1/telemetry", svc).Context())
	s.handlePostTelemetry(iw, ir)
	require.Equal(t, http.StatusAccepted, iw.Code)
	fs.mu.Lock()
	defer fs.mu.Unlock()
	require.Len(t, fs.events, 1)
	assert.Empty(t, fs.events[0].InstallID, "anonymous ingest records no identity even with a service credential present")
}
