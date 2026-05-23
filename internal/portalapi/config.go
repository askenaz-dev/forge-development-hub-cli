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

	// OTLPEndpoint is the OTel collector endpoint for trace export.
	OTLPEndpoint string
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
		OTLPEndpoint:      os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	}

	if cfg.RegistryLocalPath == "" && cfg.RegistryURL == "" {
		return cfg, fmt.Errorf("at least one of FDH_PORTAL_REGISTRY_LOCAL_PATH or FDH_PORTAL_REGISTRY_URL must be set")
	}

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
