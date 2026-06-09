package portalapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/forge/fdh/internal/portalapi/telemetry"
)

// ActivationEvent records one wizard step completion/skip. Events flow:
// the onboarding wizard's browser shim POSTs them as the user moves through
// the flow; the Go API now PERSISTS them in the shared telemetry store as
// event=activation (capability hub-usage-telemetry, design D2). Admins read
// the recent events back via GET /api/v1/admin/activation.
//
// This struct is the wire shape of the admin GET response; it is preserved
// verbatim so the admin contract is unchanged. The durable record is the
// telemetry store row — the in-memory ring buffer was removed (D2/4.3): events
// now survive a restart.
type ActivationEvent struct {
	Time            time.Time `json:"time"`
	Event           string    `json:"event"`
	Step            string    `json:"step"`
	WizardSessionID string    `json:"wizard_session_id"`
	UserID          string    `json:"user_id,omitempty"`
	Locale          string    `json:"locale,omitempty"`
	OS              string    `json:"os,omitempty"`
}

// activationRequest is the wire shape accepted by POST /api/v1/activation.
// UNCHANGED — the anonymous browser-shim contract is preserved exactly.
type activationRequest struct {
	Step            string `json:"step"`
	WizardSessionID string `json:"wizard_session_id"`
	Locale          string `json:"locale,omitempty"`
	OS              string `json:"os,omitempty"`
}

// handlePostActivation records one onboarding event. Anonymous users may submit
// events because the wizard runs pre-auth (contract preserved). The event is
// folded into the shared telemetry store as event=activation; on a store
// outage the write best-effort drops and the request still succeeds, mirroring
// the anonymous ingest contract (design D2). The activation request shape and
// the {"recorded":true} response are unchanged.
//
// NOTE: per design D4 the persisted activation row carries NO user identity —
// the authenticated user's sub is logged for operability but never written to
// the store, so activation telemetry stays pseudonymous like every other event.
func (s *Server) handlePostActivation(w http.ResponseWriter, r *http.Request) {
	var req activationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Step == "" || req.WizardSessionID == "" {
		s.writeError(w, http.StatusBadRequest, "bad_request",
			"step and wizard_session_id are required")
		return
	}
	u := userFromRequest(r)
	now := time.Now().UTC()

	// Persist as a telemetry event=activation. Best-effort: a store outage must
	// not fail the anonymous onboarding POST.
	if s.telemetry != nil {
		ev := telemetry.Event{
			Event:           "activation",
			Step:            req.Step,
			WizardSessionID: req.WizardSessionID,
			Locale:          req.Locale,
			OS:              req.OS,
			Timestamp:       now,
		}
		if err := s.telemetry.Insert(r.Context(), ev); err != nil {
			s.logger.Debug("activation persist dropped (store outage)", "err", err)
		}
	}

	s.logger.Info("activation",
		"step", req.Step,
		"wizard_session_id", req.WizardSessionID,
		"user_id", u.Sub,
		"locale", req.Locale,
		"os", req.OS,
	)
	s.writeJSON(w, http.StatusOK, map[string]any{"recorded": true})
}

// handleGetActivation returns recent activation events from the store. Admin-only
// (gated exactly as before). On a store outage it returns a typed
// store_unavailable with Retry-After rather than a 500 (portal-runtime-resilience).
func (s *Server) handleGetActivation(w http.ResponseWriter, r *http.Request) {
	u := userFromRequest(r)
	if !hasMinRole(u.Role, "admin") {
		s.writeError(w, http.StatusForbidden, "forbidden",
			"role 'admin' required")
		return
	}

	events := []ActivationEvent{}
	if s.telemetry != nil {
		rows, err := s.telemetry.ListActivation(r.Context(), 512)
		if err != nil {
			// Degraded store → retryable, not a 500.
			w.Header().Set("Retry-After", "10")
			s.writeError(w, http.StatusServiceUnavailable, "store_unavailable",
				"telemetry store is temporarily unavailable; retry shortly")
			return
		}
		events = make([]ActivationEvent, 0, len(rows))
		for _, e := range rows {
			events = append(events, ActivationEvent{
				Time:            e.Timestamp,
				Event:           "activation",
				Step:            e.Step,
				WizardSessionID: e.WizardSessionID,
				Locale:          e.Locale,
				OS:              e.OS,
			})
		}
	}

	s.writeJSON(w, http.StatusOK, map[string]any{
		"events": events,
		"count":  len(events),
	})
}
