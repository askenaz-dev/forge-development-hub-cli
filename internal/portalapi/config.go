// Package portalapi implements the FDH portal HTTP API.
//
// The package is the long-running counterpart to the CLI in `cmd/fdh/`.
// It depends on the same `pkg/registry` library so the registry contract
// is enforced exactly once.
package portalapi

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config holds the values the API needs at startup. Every field is sourced
// from an environment variable so production deploys don't need a config
// file mounted; local-dev uses the same env-driven model via .env files.
type Config struct {
	// Addr is the listen address, e.g. ":8080". Default ":8080".
	Addr string

	// RegistryLocalPath is the absolute path of the local Git clone of
	// the registry. Exactly one of LocalPath or URL must be set.
	RegistryLocalPath string

	// RegistryURL is the remote URL of the registry (clone target).
	RegistryURL string

	// RegistryBranch is the branch of the registry to track.
	RegistryBranch string

	// RefreshInterval is how often to re-read the registry. Default 60s.
	RefreshInterval time.Duration

	// OIDCDiscoveryURL points at the IdP's well-known configuration.
	// Empty disables auth (development convenience).
	OIDCDiscoveryURL string

	// OIDCClientID is the audience claim the API expects.
	OIDCClientID string

	// OIDCRoleMapPath is an optional YAML file mapping IdP claim values
	// to portal roles. Empty means every authenticated user is 'consumer'.
	OIDCRoleMapPath string

	// IDPProfile names the IdP deployment profile: "local" (self-hosted
	// in-cluster OIDC, e.g. Keycloak) or "external" (a managed OIDC provider
	// such as Entra ID / Okta / Auth0 / Google). It is informational only —
	// the portal speaks standard OIDC either way via OIDCDiscoveryURL /
	// OIDCClientID, and no code path is provider-specific. Default "local".
	IDPProfile string

	// OTLPEndpoint is the OTel collector endpoint for trace export.
	OTLPEndpoint string

	// HubPath is the absolute filesystem path where the hub catalog is
	// mounted (typically kept fresh by a git-sync sidecar). It is the source
	// of truth for the HTTP wire endpoints under /v1/*. Default "/srv/hub".
	// When the path does not exist or does not contain hub/registry.yaml,
	// the wire handlers respond 503 Service Unavailable; the rest of the
	// portal (UI endpoints, /healthz) continues to function.
	HubPath string

	// TelemetryDSN is the Postgres connection string for the shared telemetry
	// store (capability hub-usage-telemetry, design D1). It is OPTIONAL: an
	// empty DSN (or an unreachable store) disables persistence WITHOUT crashing
	// boot or blocking anonymous catalog reads — ingest best-effort drops and
	// admin reads return a typed store_unavailable (portal-runtime-resilience).
	// Sourced from FDH_TELEMETRY_DSN (Helm injects it from a Secret).
	TelemetryDSN string

	// TelemetryRetention bounds how long raw telemetry events are kept before
	// the in-process retention loop prunes them (aggregates are long-lived).
	// Default 180 days (design D6). Sourced from FDH_TELEMETRY_RETENTION.
	TelemetryRetention time.Duration

	// TelemetryAggregateInterval is how often the in-process aggregation +
	// retention loop runs on the elected (lowest-ordinal) replica. Default 1h.
	// Sourced from FDH_TELEMETRY_AGGREGATE_INTERVAL.
	TelemetryAggregateInterval time.Duration

	// FeedbackSummaryEnabled gates the OPTIONAL LLM-synthesized feedback digest
	// (capability hub-usage-telemetry, design D8). It is OFF by default and has
	// NO hard LLM dependency: when off — or on but without a configured provider
	// — GET /api/v1/admin/feedback/summary returns {enabled:false}. The raw
	// feedback list (GET /api/v1/admin/feedback) renders regardless. Sourced
	// from TELEMETRY_FEEDBACK_SUMMARY ("on"/"true"/"1" enable).
	FeedbackSummaryEnabled bool

	// FeedbackSummaryProvider names the synthesis provider/key indirection for
	// the optional feedback digest (operator-supplied; awaits org owner). When
	// empty, the summary stays {enabled:false} even if the flag is on — no LLM
	// dependency is required to ship. Sourced from TELEMETRY_FEEDBACK_PROVIDER.
	FeedbackSummaryProvider string

	// PrometheusQueryURL is an OPTIONAL external Prometheus query endpoint
	// (design D7). When set, the observability surface MAY enrich first-party
	// health with PromQL; when empty (the default), the panel renders entirely
	// from the API's own metrics + store aggregates — there is NO hard
	// Prometheus dependency. Sourced from PROMETHEUS_QUERY_URL.
	PrometheusQueryURL string
}

// LoadConfig builds a Config from environment variables, applying defaults
// for every field that has one. It returns an error only when a required
// field is missing or a value is malformed.
func LoadConfig() (Config, error) {
	cfg := Config{
		Addr:              envOr("FDH_PORTAL_API_ADDR", ":8080"),
		RegistryLocalPath: os.Getenv("FDH_PORTAL_REGISTRY_LOCAL_PATH"),
		RegistryURL:       os.Getenv("FDH_PORTAL_REGISTRY_URL"),
		RegistryBranch:    envOr("FDH_PORTAL_REGISTRY_BRANCH", "main"),
		OIDCDiscoveryURL:  os.Getenv("OIDC_DISCOVERY_URL"),
		OIDCClientID:      os.Getenv("OIDC_CLIENT_ID"),
		OIDCRoleMapPath:   os.Getenv("OIDC_ROLE_MAP_PATH"),
		IDPProfile:        envOr("FDH_PORTAL_IDP_PROFILE", "local"),
		OTLPEndpoint:      os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		HubPath:           envOr("FDH_PORTAL_HUB_PATH", "/srv/hub"),
		TelemetryDSN:      os.Getenv("FDH_TELEMETRY_DSN"),

		// Optional feedback auto-summary (design D8) — OFF by default, no LLM
		// dependency. The summary stays disabled unless BOTH the flag is on AND
		// a provider is configured.
		FeedbackSummaryEnabled:  truthyEnv("TELEMETRY_FEEDBACK_SUMMARY"),
		FeedbackSummaryProvider: strings.TrimSpace(os.Getenv("TELEMETRY_FEEDBACK_PROVIDER")),

		// Optional external Prometheus enrichment (design D7) — empty by default;
		// the observability panel renders first-party health without it.
		PrometheusQueryURL: strings.TrimSpace(os.Getenv("PROMETHEUS_QUERY_URL")),
	}

	// Telemetry retention window (raw events). Default 180 days (design D6);
	// a malformed value is non-fatal (the store is optional) — fall back.
	cfg.TelemetryRetention = 180 * 24 * time.Hour
	if v := os.Getenv("FDH_TELEMETRY_RETENTION"); v != "" {
		if d, perr := time.ParseDuration(v); perr == nil && d > 0 {
			cfg.TelemetryRetention = d
		}
	}

	// Aggregation/retention loop interval. Default 1h.
	cfg.TelemetryAggregateInterval = time.Hour
	if v := os.Getenv("FDH_TELEMETRY_AGGREGATE_INTERVAL"); v != "" {
		if d, perr := time.ParseDuration(v); perr == nil && d >= time.Minute {
			cfg.TelemetryAggregateInterval = d
		}
	}

	// The portal serves its catalog from the hub content at HubPath. The
	// legacy RegistryLocalPath/RegistryURL fields are optional (kept for the
	// CLI consumer and diagnostics) and no longer gate startup.

	intervalStr := envOr("FDH_PORTAL_REFRESH_INTERVAL", "60s")
	dur, err := time.ParseDuration(intervalStr)
	if err != nil {
		return cfg, fmt.Errorf("invalid FDH_PORTAL_REFRESH_INTERVAL %q: %w", intervalStr, err)
	}
	if dur < 5*time.Second {
		return cfg, fmt.Errorf("FDH_PORTAL_REFRESH_INTERVAL must be at least 5s (got %s)", dur)
	}
	cfg.RefreshInterval = dur

	return cfg, nil
}

// RegistrySource returns a human-readable description of the registry
// source for diagnostic output.
func (c Config) RegistrySource() string {
	if c.RegistryURL != "" {
		return c.RegistryURL
	}
	return c.RegistryLocalPath
}

// AuthEnabled reports whether OIDC validation is configured.
// When false, every request is treated as anonymous.
func (c Config) AuthEnabled() bool {
	return strings.TrimSpace(c.OIDCDiscoveryURL) != ""
}

// IDPProfileValid reports whether IDPProfile is a recognized profile.
// Unknown values are non-fatal: callers log them and proceed (the field is
// informational and never gates the anonymous catalog).
func (c Config) IDPProfileValid() bool {
	switch c.IDPProfile {
	case "local", "external":
		return true
	}
	return false
}

// FeedbackSummaryActive reports whether the optional LLM feedback digest is
// BOTH flag-enabled AND backed by a configured provider. When false, the
// summary endpoint returns {enabled:false} and no LLM is invoked (design D8) —
// there is no hard LLM dependency to ship.
func (c Config) FeedbackSummaryActive() bool {
	return c.FeedbackSummaryEnabled && c.FeedbackSummaryProvider != ""
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// truthyEnv reports whether the named env var is set to an affirmative value
// (1/true/yes/on, case-insensitive). Used for boolean feature flags.
func truthyEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// BuildInfo is link-time metadata stamped into the binary.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}
