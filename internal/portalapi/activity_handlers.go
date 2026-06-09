package portalapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Profile activity feed: the voluntary install-claim flow (capability
// hub-usage-telemetry, design D5, task 8.2). These two endpoints are the ONLY
// place the platform links a pseudonymous install_id to an identity, and ONLY
// because the signed-in user explicitly claimed it. They are admin-gated like
// every other admin surface (the BFF reaches them with the service credential,
// passing the SESSION user's own email); ingest stays anonymous and is never
// touched.
//
// Contributions (the other half of the feed, design D5) are DERIVED from Git
// authorship and served by handleGetContributions — no telemetry, already
// identity-bound. This file adds only the claimed-installs half.

// claimRequest is the body of POST /api/v1/admin/activity/claim. The web BFF
// fills `user` with the signed-in user's OWN email (the stable profile key,
// consistent with the contributions derivation); install_id is the pseudonymous
// code the user copied from `fdh telemetry claim` on their machine.
type claimRequest struct {
	InstallID string `json:"install_id"`
	User      string `json:"user"`
}

// handlePostActivityClaim implements POST /api/v1/admin/activity/claim. It is
// the single explicit identity↔telemetry link (design D5 / task 12.2): it
// inserts a row into the SEPARATE install_claims table (install_id, user, ts).
// The events table stays PII-free; nothing here reverses an install_id. Admin-
// gated; on a degraded store it returns the typed store_unavailable.
//
// Responds 202 Accepted on success (the claim is recorded; the install events
// surface in the user's feed on the next ActivityFor read).
func (s *Server) handlePostActivityClaim(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req claimRequest
	if err := dec.Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	installID := strings.TrimSpace(req.InstallID)
	user := strings.TrimSpace(req.User)
	if installID == "" || user == "" {
		s.writeError(w, http.StatusBadRequest, "bad_request",
			"install_id and user are required")
		return
	}

	if s.telemetry == nil {
		s.storeUnavailable(w)
		return
	}
	if err := s.telemetry.Claim(r.Context(), installID, normalizeEmail(user)); err != nil {
		// Any claim failure is a store/DB problem — Claim is an idempotent upsert
		// with no application-level reject path, so a live store dropping its
		// Postgres connection mid-flight is just as retryable as the degraded
		// noop store. Degrade to the typed store_unavailable like every other
		// admin handler, and never echo the raw driver error back to the caller.
		s.logger.Debug("activity claim dropped (store error)", "err", err)
		s.storeUnavailable(w)
		return
	}

	s.writeJSON(w, http.StatusAccepted, map[string]any{"claimed": true})
}

// handleGetActivity implements GET /api/v1/admin/activity?user=<email>. It
// returns the installs that user voluntarily claimed (design D5), newest first.
// Installs appear ONLY after an explicit claim — never by reversing an
// unclaimed pseudonymous id. An empty/unknown user yields an empty list (not an
// error), mirroring the contributions empty-state contract. Admin-gated.
func (s *Server) handleGetActivity(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	user := normalizeEmail(r.URL.Query().Get("user"))
	if user == "" {
		// No user → nothing to show; not an error.
		s.writeJSON(w, http.StatusOK, map[string]any{"installs": []any{}})
		return
	}
	if s.telemetry == nil {
		s.storeUnavailable(w)
		return
	}
	rows, err := s.telemetry.ActivityFor(r.Context(), user, 100)
	if err != nil {
		s.storeUnavailable(w)
		return
	}
	installs := make([]map[string]any, 0, len(rows))
	for _, ci := range rows {
		installs = append(installs, map[string]any{
			"kind":    ci.Kind,
			"name":    ci.Name,
			"version": ci.Version,
			"ts":      ci.Timestamp.UTC().Format(time.RFC3339),
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"installs": installs})
}
