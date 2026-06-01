package portalapi

import (
	"encoding/json"
	"net/http"
)

// handlePostEvents implements POST /api/v1/events — the single ingestion point
// shared by the web frontend and the CLI (BFF rule: clients post here, never
// to an analytics backend or the collector directly). Anonymous submission is
// allowed because events may originate pre-auth (e.g. onboarding).
//
// Ingestion is non-blocking: the event is validated, sanitized, and handed to
// the async emitter, which returns immediately. A slow or unavailable exporter
// never adds latency to this request.
func (s *Server) handlePostEvents(w http.ResponseWriter, r *http.Request) {
	var ev Event
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		s.writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if ev.EventName == "" {
		s.writeError(w, http.StatusBadRequest, "bad_request", "event_name is required")
		return
	}

	// Reject unknown names; drop unknown attribute keys; normalize enums and
	// the single free-form field. Anonymity is preserved by construction —
	// the envelope has no field for prompts/files/argv, and we never add a
	// user identity attribute here.
	if !normalizeEvent(&ev) {
		s.writeError(w, http.StatusBadRequest, "unknown_event", "event_name is not in the recognized taxonomy")
		return
	}

	// Identity handling: under anonymous_first we must not persist or join an
	// authenticated identity. Events carry no user_id by construction; this is
	// the explicit enforcement point in case future attributes are added.
	if s.cfg.Telemetry.AnonymousFirst() {
		delete(ev.Attributes, "user_id")
	}

	if s.events != nil {
		s.events.Emit(ev)
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"recorded": true})
}

// handleGetInsights implements GET /api/v1/admin/insights — the aggregated
// telemetry view (downloads, demand gaps, churn, install failures, feedback)
// over the retained window. Admin-only. Insights surface in the existing
// portal admin UI; no separate analytics frontend is introduced.
func (s *Server) handleGetInsights(w http.ResponseWriter, r *http.Request) {
	u := userFromRequest(r)
	if !hasMinRole(u.Role, "admin") {
		s.writeError(w, http.StatusForbidden, "forbidden", "role 'admin' required")
		return
	}
	if s.eventStore == nil {
		s.writeJSON(w, http.StatusOK, InsightsSummary{})
		return
	}
	sum := s.eventStore.Insights()
	s.writeJSON(w, http.StatusOK, map[string]any{
		"window_start":              sum.WindowStart,
		"window_end":                sum.WindowEnd,
		"total":                     sum.Total,
		"event_counts":              sum.EventCounts,
		"top_downloads":             topN(sum.Downloads, 20),
		"demand_gaps":               topN(sum.ZeroResultQ, 20),
		"top_not_found":             topN(sum.NotFound, 20),
		"top_installs":              topN(sum.Installs, 20),
		"top_uninstalls":            topN(sum.Uninstalls, 20),
		"install_failures_by_class": sum.FailuresByClass,
		"feedback":                  sum.Feedback,
	})
}
