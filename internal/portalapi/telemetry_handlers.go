package portalapi

import (
	"net/http"
	"sync"
	"time"

	"github.com/forge/fdh/internal/portalapi/telemetry"
)

// Anonymous telemetry ingest (capability hub-usage-telemetry, design D2;
// fdh-portal-api-wire-protocol). POST /api/v1/telemetry carries NO identity and
// NO BFF service-token — it mirrors the anonymous activation POST. The body is
// strict-decoded (unknown fields rejected → 400), size-capped, and rate-limited.
// Accepted events return 202; a store outage best-effort drops and STILL returns
// 202 (ingest never blocks a client). The service credential confers no
// privilege here: ingest is always anonymous, so we never inspect auth.

// ingestLimiter is a tiny fixed-window rate limiter for the anonymous ingest
// endpoint. It is intentionally dependency-light (no token-bucket library): a
// per-window counter keyed by client IP, bounding abuse without identity. The
// limiter is process-local; across replicas the aggregate cap is N×limit, which
// is acceptable for an anonymous best-effort ingest.
type ingestLimiter struct {
	mu       sync.Mutex
	window   time.Duration
	limit    int
	curStart time.Time
	counts   map[string]int
}

func newIngestLimiter(window time.Duration, limit int) *ingestLimiter {
	return &ingestLimiter{
		window:   window,
		limit:    limit,
		curStart: time.Now(),
		counts:   make(map[string]int),
	}
}

// allow reports whether a request from key may proceed in the current window.
func (l *ingestLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.curStart) >= l.window {
		l.curStart = now
		l.counts = make(map[string]int)
	}
	if l.counts[key] >= l.limit {
		return false
	}
	l.counts[key]++
	return true
}

// clientKey derives the rate-limit key from the request. It prefers the proxy's
// forwarded client IP, falling back to RemoteAddr. This is used ONLY as an
// ephemeral rate-limit bucket — it is never persisted (design D4: no IP column).
func clientKey(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First hop is the original client.
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	return r.RemoteAddr
}

// handlePostTelemetry is the anonymous ingest endpoint.
func (s *Server) handlePostTelemetry(w http.ResponseWriter, r *http.Request) {
	// Rate limit (anonymous, per-IP, ephemeral key). 429 with Retry-After when
	// the window cap is exceeded.
	if s.ingestLimiter != nil && !s.ingestLimiter.allow(clientKey(r)) {
		w.Header().Set("Retry-After", "1")
		s.writeError(w, http.StatusTooManyRequests, "rate_limited",
			"telemetry ingest rate limit exceeded; retry shortly")
		return
	}

	// Size cap: reject bodies larger than the bound before decoding.
	r.Body = http.MaxBytesReader(w, r.Body, telemetry.MaxBodyBytes)

	ev, err := telemetry.DecodeEvent(r.Body)
	if err != nil {
		// Strict decode failure (unknown field, bad enum, oversize, malformed) →
		// 400 invalid_event. Nothing is stored.
		if s.metrics != nil {
			s.metrics.recordIngest("unknown", "", "", "rejected")
		}
		s.writeError(w, http.StatusBadRequest, "invalid_event", err.Error())
		return
	}

	// Best-effort persist. On a degraded/unavailable store the write drops and
	// we STILL return 202 — ingest must never block or fail a client.
	result := "accepted"
	if s.telemetry != nil {
		if err := s.telemetry.Insert(r.Context(), ev); err != nil {
			s.logger.Debug("telemetry ingest dropped (store outage)", "err", err)
			result = "dropped"
		}
	}
	// Business metrics (task 6.1): coarse, non-identifying labels only — never
	// the install_id. Feeds /metrics and the observability surface.
	if s.metrics != nil {
		s.metrics.recordIngest(ev.Event, ev.Kind, ev.Name, result)
	}

	s.writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true})
}
