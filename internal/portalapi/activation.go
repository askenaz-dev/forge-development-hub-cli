package portalapi

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// ActivationEvent records one wizard step completion/skip. Events flow:
// the frontend POSTs them as the user moves through the onboarding flow;
// the Go API holds the most recent N in an in-memory ring buffer; admins
// can read the buffer via /api/v1/admin/activation.
//
// Persistent storage and analytics ingestion are not in scope for the
// dev-portal change — the structured log lines (with `event=activation`)
// are the durable record. The in-memory buffer is for debugging during
// the pilot.
type ActivationEvent struct {
	Time            time.Time `json:"time"`
	Event           string    `json:"event"`
	Step            string    `json:"step"`
	WizardSessionID string    `json:"wizard_session_id"`
	UserID          string    `json:"user_id,omitempty"`
	Locale          string    `json:"locale,omitempty"`
	OS              string    `json:"os,omitempty"`
}

// activationRing is a fixed-capacity ring buffer of activation events.
// New events overwrite the oldest when full.
type activationRing struct {
	mu     sync.Mutex
	events []ActivationEvent
	max    int
	cursor int
	full   bool
}

func newActivationRing(max int) *activationRing {
	if max < 16 {
		max = 16
	}
	return &activationRing{events: make([]ActivationEvent, max), max: max}
}

func (r *activationRing) push(e ActivationEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events[r.cursor] = e
	r.cursor = (r.cursor + 1) % r.max
	if r.cursor == 0 {
		r.full = true
	}
}

// snapshot returns the buffer's contents in chronological order
// (oldest first).
func (r *activationRing) snapshot() []ActivationEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		out := make([]ActivationEvent, r.cursor)
		copy(out, r.events[:r.cursor])
		return out
	}
	out := make([]ActivationEvent, 0, r.max)
	out = append(out, r.events[r.cursor:]...)
	out = append(out, r.events[:r.cursor]...)
	return out
}

// activationRequest is the wire shape accepted by POST /api/v1/activation.
type activationRequest struct {
	Step            string `json:"step"`
	WizardSessionID string `json:"wizard_session_id"`
	Locale          string `json:"locale,omitempty"`
	OS              string `json:"os,omitempty"`
}

// handlePostActivation records one event. Anonymous users may submit
// events because the wizard runs pre-auth.
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
	event := ActivationEvent{
		Time:            time.Now().UTC(),
		Event:           "activation",
		Step:            req.Step,
		WizardSessionID: req.WizardSessionID,
		UserID:          u.Sub,
		Locale:          req.Locale,
		OS:              req.OS,
	}
	if s.activation != nil {
		s.activation.push(event)
	}
	s.logger.Info("activation",
		"step", event.Step,
		"wizard_session_id", event.WizardSessionID,
		"user_id", event.UserID,
		"locale", event.Locale,
		"os", event.OS,
	)
	s.writeJSON(w, http.StatusOK, map[string]any{"recorded": true})
}

// handleGetActivation returns recent activation events. Admin-only.
func (s *Server) handleGetActivation(w http.ResponseWriter, r *http.Request) {
	u := userFromRequest(r)
	if !hasMinRole(u.Role, "admin") {
		s.writeError(w, http.StatusForbidden, "forbidden",
			"role 'admin' required")
		return
	}
	events := []ActivationEvent{}
	if s.activation != nil {
		events = s.activation.snapshot()
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"events": events,
		"count":  len(events),
	})
}
