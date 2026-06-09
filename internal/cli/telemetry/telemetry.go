// Package telemetry implements the fdh CLI's opt-in, pseudonymous usage
// telemetry: the privacy state machine (consent + enablement precedence),
// the rotating salted install-id (right-to-be-forgotten), and the
// best-effort, time-boxed event emitter.
//
// Privacy invariants enforced here (design D4 / spec hub-usage-telemetry):
//   - Default OFF. No event is emitted, no install-id is computed, and no
//     network call is made unless telemetry is affirmatively enabled.
//   - DO_NOT_TRACK forces OFF regardless of every opt-in signal.
//   - The wire payload (Event) carries NO PII: no username, email,
//     hostname, IP, repository path, or file contents. The install-id is a
//     salted hash with a locally-stored, rotatable salt that is not derived
//     from or reversible to any stable hardware, account, or identity value.
//
// All strings emitted to the user from this package are English (project
// convention: fdh CLI output is English-only).
package telemetry

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// PrivacyPolicyURL is the canonical privacy/telemetry policy the consent
// prompt and status output reference (forge-development-hub docs/privacy.md).
const PrivacyPolicyURL = "https://github.com/askenaz-dev/forge-development-hub/blob/main/docs/privacy.md"

// DefaultEndpoint is the anonymous ingest endpoint the CLI POSTs events to
// when no override is configured. It mirrors the portal API's public base.
// Overridable via FDH_TELEMETRY_ENDPOINT or the telemetry.endpoint config key.
const DefaultEndpoint = "https://hub.forge.dev/api/v1/telemetry"

// autoRotateAfter is the documented salt auto-rotation cadence (D4): the
// install-id rotates automatically after this window to bound longitudinal
// linkability, on top of the on-demand `fdh telemetry rotate`.
const autoRotateAfter = 90 * 24 * time.Hour

// stateFilename is the on-disk telemetry state under <config-dir>/fdh.
// It holds ONLY the salt and the consent/rotation bookkeeping — never any
// identity value. The install-id is derived, never stored.
const stateFilename = "telemetry.json"

// Event is the closed wire payload POSTed to /api/v1/telemetry. It is the
// single source of truth for "what we collect". The field set is a strict
// allow-list; the ingest endpoint rejects unknown fields on decode.
//
// PII GUARDRAIL: this struct MUST NOT gain a username, email, hostname, IP,
// path, or file-content field. A unit test asserts the JSON tag set against
// a frozen allow-list — adding an identifying field fails the build's tests.
type Event struct {
	Event       string `json:"event"`                  // install|download|resolve|activation|feedback
	Kind        string `json:"kind,omitempty"`         // skill|rule|agent|hook
	Namespace   string `json:"namespace,omitempty"`    // component namespace
	Name        string `json:"name,omitempty"`         // component name
	Version     string `json:"version,omitempty"`      // resolved version
	ContentHash string `json:"content_hash,omitempty"` // canonical bundle hash
	Scope       string `json:"scope,omitempty"`        // user|project
	Registry    string `json:"registry,omitempty"`     // registry source display
	OS          string `json:"os,omitempty"`           // coarse: darwin|linux|windows
	Locale      string `json:"locale,omitempty"`       // es|en
	InstallID   string `json:"install_id,omitempty"`   // pseudonymous rotating salted hash
	Timestamp   string `json:"timestamp"`              // RFC3339 UTC

	// feedback-only fields
	Rating   int    `json:"rating,omitempty"`
	Category string `json:"category,omitempty"`
	Text     string `json:"text,omitempty"`
}

// state is the persisted privacy bookkeeping. It NEVER contains an identity
// value — only the salt (random bytes, base for the pseudonymous hash) and
// the consent + rotation timestamps.
type state struct {
	// Salt is a random hex string. The install-id is sha256(salt) — rotating
	// the salt yields a fresh, uncorrelatable id (right-to-be-forgotten).
	Salt string `json:"salt"`
	// SaltRotatedAt is when the salt was last (re)generated; drives the
	// 90-day auto-rotation.
	SaltRotatedAt time.Time `json:"salt_rotated_at"`
	// ConsentAsked records that the one-time first-run consent prompt has
	// been shown, so it never recurs regardless of the answer.
	ConsentAsked bool `json:"consent_asked"`
	// ConsentGranted records the answer to the consent prompt. It is one
	// (lowest-priority) input to the enablement precedence; explicit config
	// or env overrides win over it.
	ConsentGranted bool `json:"consent_granted"`
}

// Manager owns the telemetry state file and resolves enablement. It is the
// single entry point the cli package uses; it isolates all file IO and the
// privacy precedence so the rest of the CLI never reasons about salts.
type Manager struct {
	dir string // <config-dir>/fdh
	st  state
	// loaded marks whether st reflects an on-disk read. Operations lazily
	// load so a fully-disabled CLI never touches the state file.
	loaded bool
}

// NewManager builds a Manager rooted at the given config directory
// (defaultConfigDir() in the cli package, i.e. <os.UserConfigDir>/fdh).
func NewManager(configDir string) *Manager {
	return &Manager{dir: configDir}
}

func (m *Manager) statePath() string {
	return filepath.Join(m.dir, stateFilename)
}

// load reads the state file if present. A missing/corrupt file is NOT an
// error: the Manager degrades to a zero state (default OFF, no consent).
func (m *Manager) load() {
	if m.loaded {
		return
	}
	m.loaded = true
	if m.dir == "" {
		return
	}
	data, err := os.ReadFile(m.statePath())
	if err != nil {
		return // missing → zero state
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		return // corrupt → zero state, will be rewritten on next save
	}
	m.st = st
}

// save persists the current state with restrictive-ish perms (0600 on POSIX;
// Windows ignores the bits). Best-effort: an IO failure is returned so the
// caller (an explicit command) can surface it, but the emit path ignores it.
func (m *Manager) save() error {
	if m.dir == "" {
		return fmt.Errorf("no config directory available")
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.statePath(), data, 0o600)
}

// EnablementSource explains why telemetry is on/off, for `status`.
type EnablementSource string

const (
	SourceDoNotTrack EnablementSource = "DO_NOT_TRACK"      // forced off
	SourceEnv        EnablementSource = "FDH_TELEMETRY"     // env override
	SourceConfig     EnablementSource = "telemetry.enabled" // config key
	SourceConsent    EnablementSource = "consent"           // first-run answer
	SourceDefault    EnablementSource = "default"           // unset → OFF
)

// Decision is the resolved enablement plus the deciding signal.
type Decision struct {
	Enabled bool
	Source  EnablementSource
}

// Resolve computes effective enablement from the documented precedence:
//
//	DO_NOT_TRACK (force off) > FDH_TELEMETRY > telemetry.enabled (config)
//	> consent answer > default OFF
//
// configEnabled is the resolved value of the telemetry.enabled config key as
// a tri-state: "" (unset), "true", or "false". The caller (cli package)
// reads it from viper and passes the string through so this package stays
// free of viper. getenv is injected so tests can stub the environment.
func Resolve(configEnabled string, getenv func(string) string) Decision {
	if getenv == nil {
		getenv = os.Getenv
	}
	// 1. DO_NOT_TRACK: any non-empty value forces OFF (de-facto standard).
	if v := strings.TrimSpace(getenv("DO_NOT_TRACK")); v != "" && v != "0" {
		return Decision{Enabled: false, Source: SourceDoNotTrack}
	}
	// 2. FDH_TELEMETRY: explicit env opt-in/out for the invocation.
	if v := strings.TrimSpace(getenv("FDH_TELEMETRY")); v != "" {
		return Decision{Enabled: truthy(v), Source: SourceEnv}
	}
	// 3. telemetry.enabled config key.
	switch strings.ToLower(strings.TrimSpace(configEnabled)) {
	case "true", "1", "yes", "on":
		return Decision{Enabled: true, Source: SourceConfig}
	case "false", "0", "no", "off":
		return Decision{Enabled: false, Source: SourceConfig}
	}
	return Decision{Enabled: false, Source: SourceDefault}
}

// ResolveWithConsent extends Resolve with the persisted consent answer, used
// as the lowest-priority opt-in when neither env nor config decided. It loads
// the state file lazily.
func (m *Manager) ResolveWithConsent(configEnabled string, getenv func(string) string) Decision {
	d := Resolve(configEnabled, getenv)
	if d.Source != SourceDefault {
		return d
	}
	// Only the default branch falls through to the persisted consent answer.
	m.load()
	if m.st.ConsentAsked && m.st.ConsentGranted {
		return Decision{Enabled: true, Source: SourceConsent}
	}
	return Decision{Enabled: false, Source: SourceDefault}
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// InstallID returns the pseudonymous, rotating salted install-id: the
// lowercase hex sha256 of the locally-stored random salt. It auto-rotates the
// salt if the documented window has elapsed. If telemetry is disabled the
// caller MUST NOT invoke this — it materializes (and persists) a salt on
// first use, which only happens once the user is opted in.
//
// The id is derived ONLY from the random salt: it is not seeded by hostname,
// MAC, username, or any account value, so it cannot be reversed to identity.
func (m *Manager) InstallID() (string, error) {
	m.load()
	if m.st.Salt == "" {
		if err := m.rotateLocked(); err != nil {
			return "", err
		}
	} else if time.Since(m.st.SaltRotatedAt) >= autoRotateAfter {
		// Auto-rotate to bound longitudinal linkability (D4). A failure to
		// persist the new salt is non-fatal — fall back to the current id.
		_ = m.rotateLocked()
	}
	sum := sha256.Sum256([]byte(m.st.Salt))
	return hex.EncodeToString(sum[:]), nil
}

// Rotate generates a fresh random salt, producing a new install-id that is
// not correlatable to the prior one (right-to-be-forgotten / `telemetry
// rotate`). The new salt is persisted.
func (m *Manager) Rotate() error {
	m.load()
	return m.rotateLocked()
}

// rotateLocked replaces the salt with fresh randomness and persists state.
func (m *Manager) rotateLocked() error {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	m.st.Salt = hex.EncodeToString(buf)
	m.st.SaltRotatedAt = time.Now().UTC()
	return m.save()
}

// SetEnabledConsent records an explicit enable/disable answer in the consent
// state. Used by `fdh telemetry enable|disable` as the durable, lowest-tier
// signal AND marks the consent prompt as answered so it does not recur.
// (Note: `enable`/`disable` also write the telemetry.enabled config key in
// the cli package, which outranks consent; this keeps both consistent.)
func (m *Manager) SetEnabledConsent(enabled bool) error {
	m.load()
	m.st.ConsentAsked = true
	m.st.ConsentGranted = enabled
	return m.save()
}

// ConsentAnswered reports whether the one-time consent prompt has already
// been shown (so the prompt is one-time).
func (m *Manager) ConsentAnswered() bool {
	m.load()
	return m.st.ConsentAsked
}

// RecordConsent persists the answer to the first-run consent prompt.
func (m *Manager) RecordConsent(granted bool) error {
	m.load()
	m.st.ConsentAsked = true
	m.st.ConsentGranted = granted
	return m.save()
}

// CoarseOS maps the build's GOOS to the coarse, allow-listed OS bucket
// (darwin|linux|windows). Any other GOOS is reported as its raw GOOS only if
// it is one of the three; otherwise "other" — never arch/kernel/version
// detail (D4: OS is coarse only).
func CoarseOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "darwin"
	case "windows":
		return "windows"
	case "linux":
		return "linux"
	default:
		return "other"
	}
}
