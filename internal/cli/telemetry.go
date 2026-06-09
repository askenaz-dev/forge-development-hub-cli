package cli

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/forge/fdh/internal/cli/telemetry"
)

// nowRFC3339 returns the current time as an RFC3339 UTC string for the
// telemetry timestamp field.
func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// telemetrySession bundles the per-command telemetry plumbing: the privacy
// Manager (consent + install-id), the resolved enablement Decision, and the
// best-effort Emitter. It is created once per command that may emit and is
// entirely inert when telemetry is OFF — no install-id is materialized, no
// network call is made.
type telemetrySession struct {
	mgr     *telemetry.Manager
	emitter *telemetry.Emitter
	enabled bool
}

// newTelemetrySession resolves telemetry enablement, runs the one-time
// consent prompt when appropriate, and returns a session whose Emitter is a
// no-op unless the user is opted in.
//
// Privacy precedence (telemetry.Resolve): DO_NOT_TRACK (force off) >
// FDH_TELEMETRY > telemetry.enabled (config) > consent answer > default OFF.
//
// The consent prompt only appears when (a) stdin is an interactive TTY,
// (b) no env/config signal already decided enablement, and (c) the prompt has
// not been answered before. CI / no-TTY never prompts and stays off.
func newTelemetrySession(cmd *cobra.Command) *telemetrySession {
	dir := defaultConfigDir()
	mgr := telemetry.NewManager(dir)

	configEnabled := strings.TrimSpace(viper.GetString("telemetry.enabled"))

	// First resolve without consent to learn whether an env/config signal
	// already decided — if so we must not prompt over it.
	pre := telemetry.Resolve(configEnabled, os.Getenv)
	alreadyDecided := pre.Source != telemetry.SourceDefault

	// One-time first-run consent prompt (interactive only, defaults decline).
	// Skipped entirely when a signal already decided or under no-TTY/CI.
	if !alreadyDecided && !telemetryNonInteractive() {
		_, _ = mgr.MaybePrompt(stdinIsTTY(), alreadyDecided, cmd.InOrStdin(), cmd.ErrOrStderr())
	}

	// Final decision now folds in any persisted consent answer.
	decision := mgr.ResolveWithConsent(configEnabled, os.Getenv)

	endpoint := strings.TrimSpace(viper.GetString("telemetry.endpoint"))
	if endpoint == "" {
		endpoint = telemetry.DefaultEndpoint
	}

	return &telemetrySession{
		mgr:     mgr,
		emitter: telemetry.NewEmitter(endpoint, decision.Enabled),
		enabled: decision.Enabled,
	}
}

// telemetryNonInteractive reports whether the environment indicates a
// non-interactive / CI context in which the consent prompt must never appear.
// Mirrors the CI heuristic used by the manifest install flow.
func telemetryNonInteractive() bool {
	if !stdinIsTTY() {
		return true
	}
	for _, v := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "CIRCLECI", "JENKINS_URL", "TF_BUILD"} {
		if os.Getenv(v) != "" {
			return true
		}
	}
	return false
}

// emit builds the pseudonymous, no-PII payload for a completed operation and
// enqueues it best-effort. It is a no-op when telemetry is OFF. The
// install-id is materialized lazily here (only when enabled), never earlier.
//
// kind/namespace/name/version/contentHash/scope/registry come straight from
// the InstallResult-shaped fields; os and locale are coarse buckets.
func (s *telemetrySession) emit(event, kind, namespace, name, version, contentHash, scope, registry string) {
	if s == nil || !s.enabled {
		return
	}
	id, err := s.mgr.InstallID()
	if err != nil {
		// A failure to derive the id is non-fatal: drop the event silently
		// rather than risk affecting the command.
		return
	}
	s.emitter.Enqueue(telemetry.Event{
		Event:       event,
		Kind:        kind,
		Namespace:   namespace,
		Name:        name,
		Version:     version,
		ContentHash: contentHash,
		Scope:       scope,
		Registry:    registry,
		OS:          telemetry.CoarseOS(),
		Locale:      currentLocale(),
		InstallID:   id,
		Timestamp:   nowRFC3339(),
	})
}

// flush gives queued events a brief, time-boxed chance to land. It NEVER
// affects the command's exit code or latency beyond the emitter's short time
// box. Call via defer at the end of a command.
func (s *telemetrySession) flush(ctx context.Context) {
	if s == nil || !s.enabled {
		return
	}
	wait := s.emitter.FlushAsync(ctx)
	wait()
}

// currentLocale returns the coarse UI locale bucket (es|en), defaulting to en.
// It inspects FDH_LOCALE / LANG without recording any precise locale string.
func currentLocale() string {
	for _, key := range []string{"FDH_LOCALE", "LC_ALL", "LANG"} {
		v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
		if v == "" {
			continue
		}
		if strings.HasPrefix(v, "es") {
			return "es"
		}
		if strings.HasPrefix(v, "en") {
			return "en"
		}
	}
	return "en"
}
