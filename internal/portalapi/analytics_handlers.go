package portalapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Admin analytics reads (capability hub-usage-telemetry, design D3/D6, tasks
// 5.1, 7.2-7.3, 9.2). Every endpoint here is gated EXACTLY like
// handleGetActivation — 403 unless hasMinRole(u.Role, "admin"). The BFF reaches
// them with the portal service credential (the fdh-portal-svc role-map entry
// earns `admin`), NEVER a forwarded user bearer; the same gate is the
// authoritative boundary regardless of caller.
//
// PRIVACY (design D4, spec): every analytics payload is an AGGREGATE — no
// endpoint returns a per-identity row or maps an install_id to an identity. The
// store query methods select aggregate columns only; the ingest events schema
// has no identity column to begin with. On a degraded store these reads return
// the typed store_unavailable (with Retry-After), not a 500
// (portal-runtime-resilience).

// handleGetAnalyticsSummary implements GET /api/v1/admin/analytics/summary.
// Returns the total retained-event count, the per-event-type breakdown, and the
// earliest retained timestamp. Aggregate only.
func (s *Server) handleGetAnalyticsSummary(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.telemetry == nil {
		s.storeUnavailable(w)
		return
	}
	total, byEvent, since, err := s.telemetry.SummaryByEvent(r.Context())
	if err != nil {
		s.storeUnavailable(w)
		return
	}

	// Stable, fully-populated breakdown across the closed event set, so the UI
	// always renders every bucket (zero when absent).
	breakdown := map[string]int64{
		"install":    0,
		"download":   0,
		"resolve":    0,
		"activation": 0,
		"feedback":   0,
	}
	for _, ec := range byEvent {
		if _, ok := breakdown[ec.Event]; ok {
			breakdown[ec.Event] = ec.Count
		}
	}

	sinceStr := ""
	if !since.IsZero() {
		sinceStr = since.UTC().Format(time.RFC3339)
	}

	s.writeJSON(w, http.StatusOK, map[string]any{
		"total":    total,
		"by_event": breakdown,
		"since":    sinceStr,
	})
}

// handleGetAnalyticsTop implements GET /api/v1/admin/analytics/top?metric=&limit=.
// metric is install|download (default install); limit defaults 10, capped 200.
// Returns the most-counted components as aggregate {kind,namespace,name,count}.
func (s *Server) handleGetAnalyticsTop(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.telemetry == nil {
		s.storeUnavailable(w)
		return
	}
	metric := strings.TrimSpace(r.URL.Query().Get("metric"))
	if metric != "install" && metric != "download" {
		metric = "install"
	}
	limit := 10
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	rows, err := s.telemetry.TopComponents(r.Context(), metric, limit)
	if err != nil {
		s.storeUnavailable(w)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		items = append(items, map[string]any{
			"kind":      c.Kind,
			"namespace": c.Namespace,
			"name":      c.Name,
			"count":     c.Count,
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"metric": metric,
		"items":  items,
	})
}

// handleGetAnalyticsTrends implements GET /api/v1/admin/analytics/trends?event=&days=.
// Returns per-day counts for the event over the window, oldest first.
func (s *Server) handleGetAnalyticsTrends(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.telemetry == nil {
		s.storeUnavailable(w)
		return
	}
	event := strings.TrimSpace(r.URL.Query().Get("event"))
	if event == "" {
		event = "install"
	}
	// Validate against the documented trends enum so an unknown event is a 400,
	// not a silent empty series echoed back (keeps the handler honest with the
	// OpenAPI request enum).
	switch event {
	case "install", "download", "resolve", "activation":
		// ok
	default:
		s.writeError(w, http.StatusBadRequest, "bad_request",
			"event must be one of install, download, resolve, activation")
		return
	}
	days := 30
	if v := strings.TrimSpace(r.URL.Query().Get("days")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			days = n
		}
	}

	points, err := s.telemetry.Trends(r.Context(), event, days)
	if err != nil {
		s.storeUnavailable(w)
		return
	}
	out := make([]map[string]any, 0, len(points))
	for _, p := range points {
		out = append(out, map[string]any{"date": p.Date, "count": p.Count})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"event":  event,
		"points": out,
	})
}

// handleGetAnalyticsFunnel implements GET /api/v1/admin/analytics/funnel. Returns
// the onboarding funnel steps from activation aggregates, highest first.
func (s *Server) handleGetAnalyticsFunnel(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.telemetry == nil {
		s.storeUnavailable(w)
		return
	}
	steps, err := s.telemetry.Funnel(r.Context())
	if err != nil {
		s.storeUnavailable(w)
		return
	}
	out := make([]map[string]any, 0, len(steps))
	for _, st := range steps {
		out = append(out, map[string]any{"step": st.Step, "count": st.Count})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"steps": out})
}

// handleGetFeedback implements GET /api/v1/admin/feedback?limit=&offset=. Returns
// the persisted feedback events (newest first), paginated, plus the total count.
// Renders without any LLM (design D8). Feedback rows carry no identity.
func (s *Server) handleGetFeedback(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.telemetry == nil {
		s.storeUnavailable(w)
		return
	}
	limit := 50
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if v := strings.TrimSpace(r.URL.Query().Get("offset")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	rows, total, err := s.telemetry.ListFeedback(r.Context(), limit, offset)
	if err != nil {
		s.storeUnavailable(w)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, e := range rows {
		rating := 0
		if e.Rating != nil {
			rating = *e.Rating
		}
		items = append(items, map[string]any{
			"rating":   rating,
			"category": e.Category,
			"text":     e.Text,
			"ts":       e.Timestamp.UTC().Format(time.RFC3339),
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"count": total,
	})
}

// handleGetFeedbackSummary implements GET /api/v1/admin/feedback/summary. The
// LLM-synthesized digest is OPTIONAL and feature-flagged (design D8): when the
// flag is off OR no provider is configured, it returns {enabled:false} and
// invokes NO LLM — there is no hard LLM dependency to ship. When active, it
// would return the periodically-synthesized digest; the synthesis provider is
// operator-supplied (awaits org owner), so until one is wired the active branch
// reports enabled:true with an empty digest and the generation time.
func (s *Server) handleGetFeedbackSummary(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !s.cfg.FeedbackSummaryActive() {
		// Flag off or no provider → no LLM dependency exercised.
		s.writeJSON(w, http.StatusOK, map[string]any{
			"enabled":      false,
			"summary":      "",
			"generated_at": "",
		})
		return
	}
	// Flag on AND a provider is configured. The synthesis provider integration
	// is operator-supplied; until a key/provider is wired end-to-end the digest
	// is empty but the surface reports enabled so the panel shows the section.
	s.writeJSON(w, http.StatusOK, map[string]any{
		"enabled":      true,
		"summary":      "",
		"generated_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// requireAdmin enforces the admin gate shared by every Stage-2 admin read,
// identical to handleGetActivation: 403 {"error":"forbidden",...} unless the
// principal (a human admin OR the BFF service credential mapped to admin) has
// the admin role. Returns true when the caller may proceed.
//
// Task 9.2: the BFF service credential earns `admin` via the fdh-portal-svc
// role-map entry and is accepted here — but it is NEVER inspected on the
// anonymous ingest path (handlePostTelemetry never reads auth), so a service
// token confers no privilege on ingest.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	u := userFromRequest(r)
	if !hasMinRole(u.Role, "admin") {
		s.writeError(w, http.StatusForbidden, "forbidden", "role 'admin' required")
		return false
	}
	return true
}
