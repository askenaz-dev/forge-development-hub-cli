package portalapi

import (
	"strings"
	"time"
)

// EventSchemaVersion is the current version of the event envelope wire shape.
// The envelope is the transport contract shared by the web frontend and the
// CLI; it must stay stable so the ingestion backend can later be split out of
// the portal (BFF rule) without changing any client. Bump only on a
// breaking envelope change.
const EventSchemaVersion = 1

// Event is the versioned envelope every product-telemetry event shares.
// Attributes is a flat string map whose permitted keys are constrained per
// EventName (see eventAttributeAllowlist). The envelope deliberately has no
// field for prompts, file contents, command arguments, or free-form session
// data: the content-exclusion guarantee is structural, not advisory.
type Event struct {
	SchemaVersion   int               `json:"schema_version"`
	EventName       string            `json:"event_name"`
	OccurredAt      time.Time         `json:"occurred_at"`
	InstallID       string            `json:"install_id,omitempty"`
	WizardSessionID string            `json:"wizard_session_id,omitempty"`
	Attributes      map[string]string `json:"attributes,omitempty"`
}

// Event names form a closed taxonomy across three tiers. Names outside this
// set are rejected at ingestion.
const (
	// Tier 0 — derived from server-observable signal, no client code.
	EventBundleDownloaded = "bundle.downloaded"
	EventSearchZero       = "search.zero_results"
	EventComponentMissing = "component.not_found"

	// Tier 1 — CLI lifecycle, anonymous + opt-out.
	EventComponentInstalled   = "component.installed"
	EventComponentUninstalled = "component.uninstalled"
	EventComponentUpdated     = "component.updated"
	EventInstallFailed        = "install.failed"

	// Tier 2 — voluntary feedback.
	EventFeedbackSubmitted = "feedback.submitted"
)

// eventAttributeAllowlist maps each recognized event name to the set of
// attribute keys it may carry. Keys outside the set are dropped (not
// rejected) so forward-compatible clients never break ingestion; an event
// name outside the map is rejected entirely.
var eventAttributeAllowlist = map[string]map[string]bool{
	EventBundleDownloaded: set("kind", "namespace", "name", "version"),
	EventSearchZero:       set("query", "surface", "kind"),
	EventComponentMissing: set("kind", "namespace", "name", "version"),

	EventComponentInstalled:   set("kind", "namespace", "name", "version", "os", "cli_version", "scope", "agent"),
	EventComponentUninstalled: set("kind", "namespace", "name", "version", "os", "cli_version", "scope", "agent"),
	EventComponentUpdated:     set("kind", "namespace", "name", "version", "os", "cli_version", "scope", "agent"),
	EventInstallFailed:        set("kind", "namespace", "name", "version", "os", "cli_version", "error_class"),

	EventFeedbackSubmitted: set("kind", "namespace", "name", "sentiment", "text", "surface"),
}

// validErrorClasses is the closed set accepted for install.failed's
// error_class attribute. Anything else is normalized to "other" so a buggy
// client can never smuggle free-form error text (which could leak paths or
// developer content) through this field.
var validErrorClasses = set("signature_mismatch", "network", "disk", "permission", "other")

// validSentiments is the closed set accepted for feedback.submitted.
var validSentiments = set("up", "down")

// feedbackTextMax bounds the only free-form field in the taxonomy. Feedback
// text is volunteered (Tier 2), but it is still clamped so the field cannot
// be abused as a bulk data channel.
const feedbackTextMax = 2000

// knownEventName reports whether name is part of the closed taxonomy.
func knownEventName(name string) bool {
	_, ok := eventAttributeAllowlist[name]
	return ok
}

// normalizeEvent validates and sanitizes a decoded event in place, returning
// false when the event must be rejected (unknown name). It drops attribute
// keys outside the allowlist, clamps/normalizes enumerated and free-form
// fields, and stamps OccurredAt when the client omitted it. It never adds an
// identity attribute — anonymity is preserved by construction.
func normalizeEvent(e *Event) bool {
	if !knownEventName(e.EventName) {
		return false
	}
	if e.SchemaVersion == 0 {
		e.SchemaVersion = EventSchemaVersion
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	} else {
		e.OccurredAt = e.OccurredAt.UTC()
	}

	allowed := eventAttributeAllowlist[e.EventName]
	cleaned := make(map[string]string, len(e.Attributes))
	for k, v := range e.Attributes {
		if !allowed[k] {
			continue // drop unknown keys, keep the event
		}
		cleaned[k] = v
	}

	// Normalize enumerated and free-form fields per event.
	if ec, ok := cleaned["error_class"]; ok && !validErrorClasses[ec] {
		cleaned["error_class"] = "other"
	}
	if s, ok := cleaned["sentiment"]; ok && !validSentiments[s] {
		delete(cleaned, "sentiment")
	}
	if txt, ok := cleaned["text"]; ok {
		txt = strings.TrimSpace(txt)
		if len(txt) > feedbackTextMax {
			txt = txt[:feedbackTextMax]
		}
		if txt == "" {
			delete(cleaned, "text")
		} else {
			cleaned["text"] = txt
		}
	}

	if len(cleaned) == 0 {
		e.Attributes = nil
	} else {
		e.Attributes = cleaned
	}
	return true
}

// tierOf returns the coarse tier label for an event name, used for routing
// and admin grouping. Unknown names map to "" (they never reach here).
func tierOf(name string) string {
	switch name {
	case EventBundleDownloaded, EventSearchZero, EventComponentMissing:
		return "0"
	case EventComponentInstalled, EventComponentUninstalled, EventComponentUpdated, EventInstallFailed:
		return "1"
	case EventFeedbackSubmitted:
		return "2"
	}
	return ""
}

func set(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}
