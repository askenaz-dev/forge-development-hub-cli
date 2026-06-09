package telemetry

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// MaxBodyBytes caps the ingest request body. Events are tiny; anything larger
// is malformed or abusive. The handler wraps the body in an http.MaxBytesReader
// using this bound (size-cap, design D2 / wire-protocol strict ingest).
const MaxBodyBytes = 8 << 10 // 8 KiB

// wireEvent is the EXACT closed JSON shape accepted by POST /api/v1/telemetry.
// It is the integration contract's wire event. The decoder uses
// DisallowUnknownFields, so any field NOT listed here (e.g. a stray "hostname",
// "username", "ip", or "path") makes decoding fail with a 400 — this is the
// no-PII enforcement at the ingest boundary (design D4, guardrail test).
//
// Pointers distinguish "absent" from "zero" only where it matters (rating).
type wireEvent struct {
	Event           string `json:"event"`
	Kind            string `json:"kind,omitempty"`
	Namespace       string `json:"namespace,omitempty"`
	Name            string `json:"name,omitempty"`
	Version         string `json:"version,omitempty"`
	ContentHash     string `json:"content_hash,omitempty"`
	Scope           string `json:"scope,omitempty"`
	Registry        string `json:"registry,omitempty"`
	OS              string `json:"os,omitempty"`
	Locale          string `json:"locale,omitempty"`
	InstallID       string `json:"install_id,omitempty"`
	Timestamp       string `json:"timestamp,omitempty"`
	Step            string `json:"step,omitempty"`
	WizardSessionID string `json:"wizard_session_id,omitempty"`
	Rating          *int   `json:"rating,omitempty"`
	Category        string `json:"category,omitempty"`
	Text            string `json:"text,omitempty"`
}

// validEventTypes is the closed set of accepted event kinds.
var validEventTypes = map[string]bool{
	"install":    true,
	"download":   true,
	"resolve":    true,
	"activation": true,
	"feedback":   true,
}

// validOS / validLocale enforce coarse, low-cardinality values (design D4): no
// arch/version detail beyond the OS family, and only the two supported locales.
var validOS = map[string]bool{"darwin": true, "linux": true, "windows": true}
var validLocale = map[string]bool{"es": true, "en": true}

// DecodeEvent strict-decodes a telemetry wire body into an Event, rejecting
// unknown fields and out-of-enum values. A non-nil error means a 400
// invalid_event for the caller; the body is never stored on error.
//
// The reader should already be size-capped by the handler (MaxBodyBytes).
func DecodeEvent(r io.Reader) (Event, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()

	var w wireEvent
	if err := dec.Decode(&w); err != nil {
		return Event{}, fmt.Errorf("decode: %w", err)
	}
	// Reject trailing data (a second JSON value) so the body is exactly one
	// event — defensive against smuggled payloads.
	if dec.More() {
		return Event{}, fmt.Errorf("decode: unexpected trailing data")
	}

	if !validEventTypes[w.Event] {
		return Event{}, fmt.Errorf("invalid or missing event type %q", w.Event)
	}
	if w.OS != "" && !validOS[w.OS] {
		return Event{}, fmt.Errorf("invalid os %q (want darwin|linux|windows)", w.OS)
	}
	if w.Locale != "" && !validLocale[w.Locale] {
		return Event{}, fmt.Errorf("invalid locale %q (want es|en)", w.Locale)
	}

	ts := time.Now().UTC()
	if w.Timestamp != "" {
		parsed, err := time.Parse(time.RFC3339, w.Timestamp)
		if err != nil {
			return Event{}, fmt.Errorf("invalid timestamp %q (want RFC3339)", w.Timestamp)
		}
		ts = parsed.UTC()
	}

	return Event{
		Event:           w.Event,
		Kind:            w.Kind,
		Namespace:       w.Namespace,
		Name:            w.Name,
		Version:         w.Version,
		ContentHash:     w.ContentHash,
		Scope:           w.Scope,
		Registry:        w.Registry,
		OS:              w.OS,
		Locale:          w.Locale,
		InstallID:       w.InstallID,
		Step:            w.Step,
		WizardSessionID: w.WizardSessionID,
		Rating:          w.Rating,
		Category:        w.Category,
		Text:            w.Text,
		Timestamp:       ts,
	}, nil
}
