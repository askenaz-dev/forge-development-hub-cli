package portalapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// withRequestLogging wraps the handler so every request is logged as a
// single structured JSON line with route, status, latency, user_id, and
// W3C trace context (when the inbound request carries `traceparent`).
func (s *Server) withRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		dur := time.Since(start)

		traceID := extractTraceID(r.Header.Get("traceparent"))
		u := userFromRequest(r)

		attrs := []any{
			"method", r.Method,
			"route", r.URL.Path,
			"status", rec.status,
			"latency_ms", dur.Milliseconds(),
			"remote_addr", r.RemoteAddr,
			"user_agent", r.UserAgent(),
			"user_id", u.Sub, // empty for anonymous
		}
		if traceID != "" {
			attrs = append(attrs, "trace_id", traceID)
		}
		s.logger.Info("request", attrs...)
	})
}

// withMetrics observes request duration and in-flight count for Prometheus and
// folds the request into the first-party observability stats (design D7) so the
// admin observability surface has uptime/error-rate/latency without an external
// Prometheus query source.
func (s *Server) withMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.metrics == nil {
			next.ServeHTTP(w, r)
			return
		}
		s.metrics.requestsInFlight.Inc()
		defer s.metrics.requestsInFlight.Dec()
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		dur := time.Since(start)
		s.metrics.observeRequest(routeLabel(r.URL.Path), r.Method, rec.status, dur)
		s.obs.record(rec.status, float64(dur.Microseconds())/1000.0)
	})
}

// extractTraceID parses the trace-id segment of a W3C traceparent header.
// Returns "" if the header is missing or malformed. We do not bring in the
// full OTel SDK in M3 — just enough to propagate context to logs. The OTel
// SDK + OTLP export lands in M10.
func extractTraceID(traceparent string) string {
	if traceparent == "" {
		return ""
	}
	// Format: version-trace_id-parent_id-flags
	parts := strings.Split(traceparent, "-")
	if len(parts) < 4 {
		return ""
	}
	if len(parts[1]) != 32 {
		return ""
	}
	return parts[1]
}

// routeLabel normalizes path variables to placeholders so the metric
// cardinality stays bounded (e.g. /api/v1/skills/foo/bar all collapse to
// /api/v1/skills/{namespace}/{name}).
func routeLabel(path string) string {
	// Quick prefix match: anything under /api/v1/skills/<ns>/<name>... gets
	// the canonical template.
	if strings.HasPrefix(path, "/api/v1/skills/") {
		remainder := strings.TrimPrefix(path, "/api/v1/skills/")
		parts := strings.Split(remainder, "/")
		switch len(parts) {
		case 2:
			return "/api/v1/skills/{namespace}/{name}"
		case 4:
			if parts[2] == "versions" {
				return "/api/v1/skills/{namespace}/{name}/versions/{version}"
			}
		case 5:
			if parts[2] == "versions" && parts[4] == "skill-md" {
				return "/api/v1/skills/{namespace}/{name}/versions/{version}/skill-md"
			}
		}
	}
	return path
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// parseOffsetCursor decodes the opaque cursor used by handleListSkills.
// For the MVP the cursor is just an integer offset; future versions can
// upgrade to keyset cursors without breaking the wire contract.
func parseOffsetCursor(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid cursor")
	}
	return n, nil
}

func encodeOffsetCursor(n int) string {
	return strconv.Itoa(n)
}
