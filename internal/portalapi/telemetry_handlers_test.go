package portalapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/internal/portalapi/auth"
	"github.com/forge/fdh/internal/portalapi/telemetry"
)

// fakeStore is an in-memory telemetry.Store for handler tests. It lets us assert
// persistence and the activation round-trip WITHOUT a live Postgres, and can be
// flipped to a degraded mode to exercise store-outage behavior.
type fakeStore struct {
	mu        sync.Mutex
	events    []telemetry.Event
	claims    []fakeClaim // separate identity↔telemetry link (never merged into events)
	available bool
	degraded  bool // when true, reads return ErrStoreUnavailable
}

func newFakeStore() *fakeStore { return &fakeStore{available: true} }

func (f *fakeStore) Insert(ctx context.Context, e telemetry.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.degraded {
		// Mirror a real best-effort drop semantics from the caller's view: the
		// pgx store would return an error here, which the handler swallows.
		return assertErr("store down")
	}
	f.events = append(f.events, e)
	return nil
}

func (f *fakeStore) ListActivation(ctx context.Context, limit int) ([]telemetry.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.degraded {
		return nil, telemetry.ErrStoreUnavailable
	}
	out := []telemetry.Event{}
	for i := len(f.events) - 1; i >= 0 && len(out) < limit; i-- {
		if f.events[i].Event == "activation" {
			out = append(out, f.events[i])
		}
	}
	return out, nil
}

func (f *fakeStore) Aggregate(ctx context.Context, retention time.Duration) error {
	if f.degraded {
		return telemetry.ErrStoreUnavailable
	}
	return nil
}

// --- Stage-2 analytics/feedback/activity reads on the fake store ---

func (f *fakeStore) SummaryByEvent(ctx context.Context) (int64, []telemetry.EventCount, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.degraded {
		return 0, nil, time.Time{}, telemetry.ErrStoreUnavailable
	}
	counts := map[string]int64{}
	var since time.Time
	for _, e := range f.events {
		counts[e.Event]++
		if since.IsZero() || (!e.Timestamp.IsZero() && e.Timestamp.Before(since)) {
			since = e.Timestamp
		}
	}
	var total int64
	out := make([]telemetry.EventCount, 0, len(counts))
	for ev, c := range counts {
		total += c
		out = append(out, telemetry.EventCount{Event: ev, Count: c})
	}
	return total, out, since, nil
}

func (f *fakeStore) TopComponents(ctx context.Context, metric string, limit int) ([]telemetry.ComponentCount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.degraded {
		return nil, telemetry.ErrStoreUnavailable
	}
	type key struct{ kind, ns, name string }
	agg := map[key]int64{}
	for _, e := range f.events {
		if e.Event != metric {
			continue
		}
		agg[key{e.Kind, e.Namespace, e.Name}]++
	}
	out := make([]telemetry.ComponentCount, 0, len(agg))
	for k, c := range agg {
		out = append(out, telemetry.ComponentCount{Kind: k.kind, Namespace: k.ns, Name: k.name, Count: c})
	}
	return out, nil
}

func (f *fakeStore) Trends(ctx context.Context, event string, days int) ([]telemetry.TrendPoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.degraded {
		return nil, telemetry.ErrStoreUnavailable
	}
	byDay := map[string]int64{}
	for _, e := range f.events {
		if e.Event != event {
			continue
		}
		byDay[e.Timestamp.UTC().Format("2006-01-02")]++
	}
	out := make([]telemetry.TrendPoint, 0, len(byDay))
	for d, c := range byDay {
		out = append(out, telemetry.TrendPoint{Date: d, Count: c})
	}
	return out, nil
}

func (f *fakeStore) Funnel(ctx context.Context) ([]telemetry.FunnelStep, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.degraded {
		return nil, telemetry.ErrStoreUnavailable
	}
	byStep := map[string]int64{}
	for _, e := range f.events {
		if e.Event != "activation" {
			continue
		}
		byStep[e.Step]++
	}
	out := make([]telemetry.FunnelStep, 0, len(byStep))
	for st, c := range byStep {
		out = append(out, telemetry.FunnelStep{Step: st, Count: c})
	}
	return out, nil
}

func (f *fakeStore) EventCount(ctx context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.degraded {
		return 0, telemetry.ErrStoreUnavailable
	}
	return int64(len(f.events)), nil
}

func (f *fakeStore) ListFeedback(ctx context.Context, limit, offset int) ([]telemetry.Event, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.degraded {
		return nil, 0, telemetry.ErrStoreUnavailable
	}
	var fb []telemetry.Event
	for i := len(f.events) - 1; i >= 0; i-- {
		if f.events[i].Event == "feedback" {
			fb = append(fb, f.events[i])
		}
	}
	total := len(fb)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if limit <= 0 || end > total {
		end = total
	}
	return fb[offset:end], total, nil
}

// claims is the fake's separate identity↔telemetry link, mirroring the real
// install_claims table: it is NEVER merged into f.events (which stays PII-free).
type fakeClaim struct{ installID, user string }

func (f *fakeStore) Claim(ctx context.Context, installID, userEmail string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.degraded {
		return telemetry.ErrStoreUnavailable
	}
	f.claims = append(f.claims, fakeClaim{installID: installID, user: userEmail})
	return nil
}

func (f *fakeStore) ActivityFor(ctx context.Context, userEmail string, limit int) ([]telemetry.ClaimedInstall, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.degraded {
		return nil, telemetry.ErrStoreUnavailable
	}
	claimed := map[string]bool{}
	for _, c := range f.claims {
		if c.user == userEmail {
			claimed[c.installID] = true
		}
	}
	var out []telemetry.ClaimedInstall
	for i := len(f.events) - 1; i >= 0; i-- {
		e := f.events[i]
		if e.Event == "install" && claimed[e.InstallID] {
			out = append(out, telemetry.ClaimedInstall{
				Kind: e.Kind, Name: e.Name, Version: e.Version, Timestamp: e.Timestamp,
			})
		}
	}
	return out, nil
}

func (f *fakeStore) Available() bool { return f.available }
func (f *fakeStore) Close() error    { return nil }

func (f *fakeStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

// telemetryTestServer returns a minimal *Server wired with the given store and a
// fresh ingest limiter — enough to exercise the telemetry handlers directly.
func telemetryTestServer(store telemetry.Store) *Server {
	s := &Server{telemetry: store, ingestLimiter: newIngestLimiter(time.Minute, 1000)}
	s.logger = slogDiscard()
	return s
}

// --- POST /api/v1/telemetry ---

func TestIngest_ValidEvent_202(t *testing.T) {
	fs := newFakeStore()
	s := telemetryTestServer(fs)

	body := `{"event":"install","kind":"skill","namespace":"forge","name":"ds",` +
		`"version":"1.0.0","os":"linux","locale":"en","timestamp":"2026-06-08T00:00:00Z"}`
	w := httptest.NewRecorder()
	s.handlePostTelemetry(w, httptest.NewRequest(http.MethodPost, "/api/v1/telemetry", strings.NewReader(body)))

	require.Equal(t, http.StatusAccepted, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, 1, fs.count(), "valid event must be persisted")
}

func TestIngest_UnknownField_400(t *testing.T) {
	fs := newFakeStore()
	s := telemetryTestServer(fs)

	// "hostname" is not in the closed schema → strict decode rejects it.
	body := `{"event":"install","hostname":"my-laptop"}`
	w := httptest.NewRecorder()
	s.handlePostTelemetry(w, httptest.NewRequest(http.MethodPost, "/api/v1/telemetry", strings.NewReader(body)))

	require.Equal(t, http.StatusBadRequest, w.Code)
	var env map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.Equal(t, "invalid_event", env["error"])
	assert.Equal(t, 0, fs.count(), "rejected event must not be stored")
}

// TestIngest_StoreOutage_Drops202 proves a valid event during a store outage is
// best-effort dropped and STILL returns 202 — ingest never blocks a client.
func TestIngest_StoreOutage_Drops202(t *testing.T) {
	fs := newFakeStore()
	fs.degraded = true // Insert returns an error; the handler must swallow it
	s := telemetryTestServer(fs)

	body := `{"event":"resolve","os":"darwin","locale":"es"}`
	w := httptest.NewRecorder()
	s.handlePostTelemetry(w, httptest.NewRequest(http.MethodPost, "/api/v1/telemetry", strings.NewReader(body)))

	require.Equal(t, http.StatusAccepted, w.Code, "store outage must not turn ingest into 500")
}

// TestIngest_OversizeBody_400 proves the size cap rejects an oversized body.
func TestIngest_OversizeBody_400(t *testing.T) {
	fs := newFakeStore()
	s := telemetryTestServer(fs)

	huge := `{"event":"install","text":"` + strings.Repeat("x", telemetry.MaxBodyBytes+1024) + `"}`
	w := httptest.NewRecorder()
	s.handlePostTelemetry(w, httptest.NewRequest(http.MethodPost, "/api/v1/telemetry", strings.NewReader(huge)))

	require.Equal(t, http.StatusBadRequest, w.Code)
}

// TestIngest_RateLimited_429 proves the per-IP fixed-window limiter trips.
func TestIngest_RateLimited_429(t *testing.T) {
	fs := newFakeStore()
	s := &Server{telemetry: fs, ingestLimiter: newIngestLimiter(time.Minute, 2)}
	s.logger = slogDiscard()

	send := func() int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/telemetry",
			strings.NewReader(`{"event":"download"}`))
		r.RemoteAddr = "10.0.0.5:1234"
		s.handlePostTelemetry(w, r)
		return w.Code
	}
	assert.Equal(t, http.StatusAccepted, send())
	assert.Equal(t, http.StatusAccepted, send())
	assert.Equal(t, http.StatusTooManyRequests, send(), "third request in window must be rate-limited")
}

// --- POST /api/v1/activation folded into the store ---

// TestActivation_PostPersistsAsEvent proves the preserved anonymous activation
// POST routes into the store as event=activation and keeps its exact response.
func TestActivation_PostPersistsAsEvent(t *testing.T) {
	fs := newFakeStore()
	s := telemetryTestServer(fs)

	body := `{"step":"install-cli","wizard_session_id":"sess-1","locale":"en","os":"linux"}`
	w := httptest.NewRecorder()
	s.handlePostActivation(w, httptest.NewRequest(http.MethodPost, "/api/v1/activation", strings.NewReader(body)))

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var env map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.Equal(t, true, env["recorded"], "anonymous activation response contract preserved")

	require.Equal(t, 1, fs.count())
	require.Equal(t, "activation", fs.events[0].Event)
	assert.Equal(t, "install-cli", fs.events[0].Step)
	assert.Equal(t, "sess-1", fs.events[0].WizardSessionID)
	// Privacy: the persisted activation row carries no identity.
	assert.Empty(t, fs.events[0].InstallID)
}

// TestActivation_PostMissingFields_400 preserves the existing validation.
func TestActivation_PostMissingFields_400(t *testing.T) {
	s := telemetryTestServer(newFakeStore())
	w := httptest.NewRecorder()
	s.handlePostActivation(w, httptest.NewRequest(http.MethodPost, "/api/v1/activation",
		strings.NewReader(`{"step":""}`)))
	require.Equal(t, http.StatusBadRequest, w.Code)
}

// --- GET /api/v1/admin/activation reads the store ---

func TestActivationGet_AdminReadsStore(t *testing.T) {
	fs := newFakeStore()
	s := telemetryTestServer(fs)
	// Seed one activation event via the POST path.
	pw := httptest.NewRecorder()
	s.handlePostActivation(pw, httptest.NewRequest(http.MethodPost, "/api/v1/activation",
		strings.NewReader(`{"step":"done","wizard_session_id":"s9","locale":"es","os":"darwin"}`)))
	require.Equal(t, http.StatusOK, pw.Code)

	w := httptest.NewRecorder()
	s.handleGetActivation(w, requestAs("/api/v1/admin/activation",
		auth.User{Role: auth.RoleAdmin, Sub: "admin1"}))

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var env struct {
		Events []ActivationEvent `json:"events"`
		Count  int               `json:"count"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	require.Equal(t, 1, env.Count)
	assert.Equal(t, "done", env.Events[0].Step)
	assert.Equal(t, "activation", env.Events[0].Event)
}

func TestActivationGet_NonAdmin_403(t *testing.T) {
	s := telemetryTestServer(newFakeStore())
	w := httptest.NewRecorder()
	s.handleGetActivation(w, requestAs("/api/v1/admin/activation", auth.Anonymous()))
	require.Equal(t, http.StatusForbidden, w.Code)
}

// TestActivationGet_StoreOutage_503 proves the admin read degrades to a typed
// store_unavailable with Retry-After, not a 500 (portal-runtime-resilience).
func TestActivationGet_StoreOutage_503(t *testing.T) {
	fs := newFakeStore()
	fs.degraded = true
	s := telemetryTestServer(fs)

	w := httptest.NewRecorder()
	s.handleGetActivation(w, requestAs("/api/v1/admin/activation",
		auth.User{Role: auth.RoleAdmin, Sub: "admin1"}))

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.NotEmpty(t, w.Header().Get("Retry-After"))
	var env map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.Equal(t, "store_unavailable", env["error"])
}
