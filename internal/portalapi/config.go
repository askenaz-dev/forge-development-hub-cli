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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// BuildInfo is link-time metadata stamped into the binary.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}
