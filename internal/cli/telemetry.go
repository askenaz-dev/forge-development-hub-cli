package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Telemetry is opt-out, anonymous, and event-level. The CLI emits only the
// component coordinate plus enumerated outcome fields — never prompts, file
// contents, command arguments, or any free-form text. The portal's
// /api/v1/events endpoint is the single ingress (BFF); there is no direct
// analytics backend address here.

// telemetryEventSchemaVersion mirrors portalapi.EventSchemaVersion. Kept as a
// local constant so the CLI has no import dependency on the portal package.
const telemetryEventSchemaVersion = 1

// Event names mirror the portal's taxonomy (Tier 1 lifecycle).
const (
	EventNameInstalled     = "component.installed"
	EventNameUninstalled   = "component.uninstalled"
	EventNameUpdated       = "component.updated"
	EventNameInstallFailed = "install.failed"
	EventFeedbackName      = "feedback.submitted"
)

// goos returns the host OS label attached to lifecycle events.
func goos() string { return runtime.GOOS }

// telemetryEvent is the wire envelope POSTed to the portal. It must stay
// structurally identical to the portal's Event type.
type telemetryEvent struct {
	SchemaVersion int               `json:"schema_version"`
	EventName     string            `json:"event_name"`
	OccurredAt    time.Time         `json:"occurred_at"`
	InstallID     string            `json:"install_id,omitempty"`
	Attributes    map[string]string `json:"attributes,omitempty"`
}

// telemetryEnabled reports whether the CLI may emit events. Telemetry is on by
// default (opt-out). It is disabled when DO_NOT_TRACK is set to a truthy value
// or when the user set telemetry.enabled=false.
func telemetryEnabled() bool {
	if doNotTrack() {
		return false
	}
	// viper default for telemetry.enabled is true (set in initConfig).
	return viper.GetBool("telemetry.enabled")
}

// doNotTrack honors the cross-tool DO_NOT_TRACK convention: any non-empty
// value other than "0"/"false" disables telemetry.
func doNotTrack() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("DO_NOT_TRACK")))
	return v != "" && v != "0" && v != "false"
}

// telemetryEndpoint resolves the portal events endpoint. An explicit
// telemetry.endpoint wins; otherwise it is derived from an HTTP registry.url
// (scheme://host/api/v1/events). Returns "" when no HTTP origin is known, in
// which case emission is skipped entirely (e.g. git-transport pilots).
func telemetryEndpoint() string {
	if ep := strings.TrimSpace(viper.GetString("telemetry.endpoint")); ep != "" {
		return ep
	}
	regURL := strings.TrimSpace(viper.GetString("registry.url"))
	if !isHTTPURL(regURL) {
		return ""
	}
	u, err := url.Parse(regURL)
	if err != nil {
		return ""
	}
	u.Path = "/api/v1/events"
	u.RawQuery = ""
	return u.String()
}

// installID returns the anonymous, persistent installation identifier. It is a
// random value derived from no PII (no hostname, MAC, or username). It is
// generated on first use and stored in the config dir; resetInstallID rotates
// it. A failure to persist degrades to an ephemeral id so telemetry never
// blocks a command.
func installID() string {
	path := installIDPath()
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			if id := strings.TrimSpace(string(data)); id != "" {
				return id
			}
		}
	}
	id := randomID()
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			_ = os.WriteFile(path, []byte(id), 0o644)
		}
	}
	return id
}

func installIDPath() string {
	dir := defaultConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "install_id")
}

// randomID returns 16 random bytes hex-encoded — no PII, not derived from any
// host attribute.
func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Extremely unlikely; fall back to a time-seeded value that is still
		// not PII.
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// emitTelemetry sends one event to the portal, best-effort. It never blocks
// the command meaningfully (bounded timeout), never returns an error, and is a
// no-op when telemetry is disabled or no endpoint is known. Network failures
// are swallowed (surfaced only under --verbose).
func emitTelemetry(cmd *cobra.Command, name string, attrs map[string]string) {
	if !telemetryEnabled() {
		return
	}
	endpoint := telemetryEndpoint()
	if endpoint == "" {
		return
	}
	ev := telemetryEvent{
		SchemaVersion: telemetryEventSchemaVersion,
		EventName:     name,
		OccurredAt:    time.Now().UTC(),
		InstallID:     installID(),
		Attributes:    attrs,
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		verboseTelemetryLog(cmd, "emit failed: %v", err)
		return
	}
	_ = resp.Body.Close()
}

// lifecycleAttrs builds the standard coordinate + outcome attribute set shared
// by install/uninstall/update events. It carries only enumerated fields.
func lifecycleAttrs(kind, namespace, name, version, scope string, agents []string, info BuildInfo) map[string]string {
	a := map[string]string{
		"kind":        kind,
		"namespace":   namespace,
		"name":        name,
		"version":     version,
		"os":          runtime.GOOS,
		"cli_version": info.Version,
	}
	if scope != "" {
		a["scope"] = scope
	}
	if len(agents) > 0 {
		a["agent"] = strings.Join(agents, ",")
	}
	return a
}

// classifyErrorClass maps a CLI error to the closed error_class taxonomy. It
// inspects the stable exit code and, for generic failures, a couple of
// well-known substrings — never the raw error text (which could leak paths).
func classifyErrorClass(err error) string {
	switch ExitCode(err) {
	case ExitRegistryUnreach:
		return "network"
	case ExitPermission:
		return "permission"
	}
	if errors.Is(err, os.ErrPermission) {
		return "permission"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "signature"):
		return "signature_mismatch"
	case strings.Contains(msg, "no space") || strings.Contains(msg, "disk"):
		return "disk"
	}
	return "other"
}

// maybeShowFirstRunNotice prints a one-time notice describing what telemetry
// collects and how to disable it, then records that it was shown. It is a
// no-op when telemetry is disabled or the notice was already shown.
func maybeShowFirstRunNotice(cmd *cobra.Command) {
	if !telemetryEnabled() {
		return
	}
	marker := telemetryNoticePath()
	if marker == "" {
		return
	}
	if _, err := os.Stat(marker); err == nil {
		return // already shown
	}
	fmt.Fprintln(cmd.ErrOrStderr(), telemetryNoticeText)
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err == nil {
		_ = os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)
	}
}

func telemetryNoticePath() string {
	dir := defaultConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, ".telemetry-notice")
}

const telemetryNoticeText = `fdh collects anonymous usage telemetry to help improve the hub: which
components are installed/updated/uninstalled and whether installs fail. It
NEVER collects your prompts, file contents, or command arguments. Disable it
any time with 'fdh config telemetry off' or by setting DO_NOT_TRACK=1.`

func verboseTelemetryLog(cmd *cobra.Command, format string, args ...any) {
	if v, _ := cmd.Flags().GetBool("verbose"); v {
		fmt.Fprintf(cmd.ErrOrStderr(), "[telemetry] "+format+"\n", args...)
	}
}
