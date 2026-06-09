package portalapi

import (
	"net/http"
	"sort"
	"sync"
	"time"
)

// First-party observability (capability hub-usage-telemetry, design D7 / tasks
// 6.1-6.3). The admin observability surface reports site/component health —
// uptime, request totals, error rate, latency percentiles, store health, and
// per-component scan status — derived from the API's OWN metrics plus the
// telemetry store aggregates. There is NO hard external-Prometheus dependency:
// an optional PROMETHEUS_QUERY_URL may enrich the panel, but absent it the
// surface renders entirely from first-party data.

// obsReservoirSize bounds the in-memory latency reservoir. A small ring is
// enough to estimate p50/p95 cheaply without pulling samples back out of the
// Prometheus histogram (which is awkward to query in-process).
const obsReservoirSize = 1024

// obsStats accumulates first-party request statistics for the observability
// endpoint. It is updated by the metrics middleware on every request and read
// (snapshotted) by the observability handler. All counters are process-local;
// across replicas the surface reflects the replica that served the read.
type obsStats struct {
	mu        sync.Mutex
	total     uint64
	errors    uint64    // responses with status >= 500
	clientErr uint64    // responses 400-499 (not counted toward the SLO error rate)
	latencies []float64 // ring of recent latency_ms samples
	next      int       // ring write cursor
	filled    bool      // whether the ring has wrapped at least once
}

func newObsStats() *obsStats {
	return &obsStats{latencies: make([]float64, 0, obsReservoirSize)}
}

// record folds one served request into the stats. latencyMS is the wall-clock
// handler latency in milliseconds. Only server errors (5xx) count toward the
// error rate; 4xx are client faults tracked separately.
func (o *obsStats) record(status int, latencyMS float64) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.total++
	switch {
	case status >= 500:
		o.errors++
	case status >= 400:
		o.clientErr++
	}
	if len(o.latencies) < obsReservoirSize {
		o.latencies = append(o.latencies, latencyMS)
		return
	}
	o.latencies[o.next] = latencyMS
	o.next = (o.next + 1) % obsReservoirSize
	o.filled = true
}

// obsSnapshot is an immutable read of the stats at a point in time.
type obsSnapshot struct {
	total     uint64
	errors    uint64
	errorRate float64
	p50       float64
	p95       float64
}

// snapshot computes totals, error rate, and latency percentiles.
func (o *obsStats) snapshot() obsSnapshot {
	if o == nil {
		return obsSnapshot{}
	}
	o.mu.Lock()
	samples := make([]float64, len(o.latencies))
	copy(samples, o.latencies)
	total := o.total
	errors := o.errors
	o.mu.Unlock()

	var rate float64
	if total > 0 {
		rate = float64(errors) / float64(total)
	}
	return obsSnapshot{
		total:     total,
		errors:    errors,
		errorRate: rate,
		p50:       percentile(samples, 0.50),
		p95:       percentile(samples, 0.95),
	}
}

// percentile returns the p-th percentile (0..1) of samples using nearest-rank.
// Returns 0 for an empty slice. It sorts a copy so the caller's slice is intact.
func percentile(samples []float64, p float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := make([]float64, len(samples))
	copy(sorted, samples)
	sort.Float64s(sorted)
	idx := int(p * float64(len(sorted)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// handleGetObservability implements GET /api/v1/admin/observability. Admin-only
// (gated exactly like handleGetActivation). It reports site health from the
// API's own request stats + process uptime, store health + retained event count
// from the telemetry store, and per-component scan status from the live catalog
// snapshot. A degraded store does NOT fail the read — the store block reports
// available:false and event_count:0 while site health still renders (the panel
// must render without an external query source, design D7).
func (s *Server) handleGetObservability(w http.ResponseWriter, r *http.Request) {
	u := userFromRequest(r)
	if !hasMinRole(u.Role, "admin") {
		s.writeError(w, http.StatusForbidden, "forbidden", "role 'admin' required")
		return
	}

	snap := s.obs.snapshot()

	uptime := int64(0)
	if !s.startedAt.IsZero() {
		uptime = int64(time.Since(s.startedAt).Seconds())
	}

	// Store health: available + retained event count. A degraded store yields
	// available:false, count 0 — never an error here (observability must render).
	storeAvailable := false
	var eventCount int64
	if s.telemetry != nil {
		storeAvailable = s.telemetry.Available()
		if storeAvailable {
			if n, err := s.telemetry.EventCount(r.Context()); err == nil {
				eventCount = n
			} else {
				// The store reported available but the count failed; treat as a
				// soft signal, not a 500 — the panel still renders site health.
				storeAvailable = false
			}
		}
	}

	// Keep the /metrics gauge honest: drive fdh_portal_api_telemetry_store_up
	// from the just-observed health so it reflects a RUNTIME outage, not only the
	// boot snapshot (setStoreUp is otherwise called once at construction, and a
	// live pgxStore.Available() returns true unconditionally).
	s.metrics.setStoreUp(storeAvailable)

	// Per-component health from the live catalog snapshot (scan status).
	components := []map[string]any{}
	if cs := s.Snapshot(); cs != nil {
		for _, e := range cs.index.Components {
			components = append(components, map[string]any{
				"kind":        e.Kind,
				"name":        e.Name,
				"scan_status": e.ScanStatus,
			})
		}
	}

	body := map[string]any{
		"uptime_seconds": uptime,
		"requests_total": snap.total,
		"error_rate":     round4(snap.errorRate),
		"latency_ms": map[string]any{
			"p50": round2(snap.p50),
			"p95": round2(snap.p95),
		},
		"store": map[string]any{
			"available":   storeAvailable,
			"event_count": eventCount,
		},
		"components": components,
	}
	// Optional external Prometheus enrichment is advertised but never required
	// (design D7); the panel renders fully from first-party data above.
	if s.cfg.PrometheusQueryURL != "" {
		body["prometheus_query_url"] = s.cfg.PrometheusQueryURL
	}

	s.writeJSON(w, http.StatusOK, body)
}

// round2 / round4 trim float noise in the JSON payload.
func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }
func round4(f float64) float64 { return float64(int64(f*10000+0.5)) / 10000 }

// storeUnavailable writes the typed store_unavailable response with a
// Retry-After header used by every admin analytics/feedback/claim read+write on
// a degraded store (portal-runtime-resilience — NOT a 500). It is the single
// place the retryable-degradation contract is expressed for the Stage-2 surface.
func (s *Server) storeUnavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "10")
	s.writeError(w, http.StatusServiceUnavailable, "store_unavailable",
		"telemetry store is temporarily unavailable; retry shortly")
}
