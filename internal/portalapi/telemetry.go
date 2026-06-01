package portalapi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Privacy posture modes.
const (
	TelemetryModeInternal = "internal"
	TelemetryModePublic   = "public"
)

// IP handling strategies applied before a client address is written to a log
// line or attached to anything.
const (
	IPFull     = "full"
	IPTruncate = "truncate"
	IPHash     = "hash"
	IPDrop     = "drop"
)

// Identity strategies.
const (
	IdentityAttributed     = "attributed"
	IdentityAnonymousFirst = "anonymous_first"
)

// TelemetryConfig is the single privacy-posture switch for the deployment.
// Mode sets the defaults for IPHandling, Identity, and RetentionDays; each
// default is individually overridable via its own env var.
type TelemetryConfig struct {
	Mode          string
	IPHandling    string
	Identity      string
	RetentionDays int
	OTLPEndpoint  string

	// ipHashSalt salts the hashed-IP strategy so the digest is not a plain,
	// rainbow-tableable SHA of a public IP. Sourced once at load.
	ipHashSalt string
}

// loadTelemetryConfig builds the telemetry config from environment, applying
// mode-derived defaults and honoring explicit per-field overrides.
func loadTelemetryConfig() (TelemetryConfig, error) {
	mode := strings.ToLower(envOr("FDH_TELEMETRY_MODE", TelemetryModePublic))
	if mode != TelemetryModeInternal && mode != TelemetryModePublic {
		return TelemetryConfig{}, fmt.Errorf("FDH_TELEMETRY_MODE must be %q or %q (got %q)",
			TelemetryModeInternal, TelemetryModePublic, mode)
	}

	tc := TelemetryConfig{Mode: mode}
	// Mode-derived defaults.
	switch mode {
	case TelemetryModePublic:
		tc.IPHandling = IPTruncate
		tc.Identity = IdentityAnonymousFirst
		tc.RetentionDays = 30
	case TelemetryModeInternal:
		tc.IPHandling = IPFull
		tc.Identity = IdentityAttributed
		tc.RetentionDays = 90
	}

	// Explicit overrides win over the mode default.
	if v := os.Getenv("FDH_TELEMETRY_IP_HANDLING"); v != "" {
		tc.IPHandling = strings.ToLower(v)
	}
	if v := os.Getenv("FDH_TELEMETRY_IDENTITY"); v != "" {
		tc.Identity = strings.ToLower(v)
	}
	if v := os.Getenv("FDH_TELEMETRY_RETENTION_DAYS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return TelemetryConfig{}, fmt.Errorf("invalid FDH_TELEMETRY_RETENTION_DAYS %q", v)
		}
		tc.RetentionDays = n
	}
	// OTEL_EXPORTER_OTLP_ENDPOINT is the OTel-standard variable; keep parity
	// with Config.OTLPEndpoint which reads the same.
	tc.OTLPEndpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")

	switch tc.IPHandling {
	case IPFull, IPTruncate, IPHash, IPDrop:
	default:
		return TelemetryConfig{}, fmt.Errorf("invalid FDH_TELEMETRY_IP_HANDLING %q", tc.IPHandling)
	}
	switch tc.Identity {
	case IdentityAttributed, IdentityAnonymousFirst:
	default:
		return TelemetryConfig{}, fmt.Errorf("invalid FDH_TELEMETRY_IDENTITY %q", tc.Identity)
	}

	tc.ipHashSalt = envOr("FDH_TELEMETRY_IP_HASH_SALT", "fdh-telemetry")
	return tc, nil
}

// Retention returns the retention window as a duration (0 means unbounded).
func (tc TelemetryConfig) Retention() time.Duration {
	return time.Duration(tc.RetentionDays) * 24 * time.Hour
}

// AnonymousFirst reports whether identity must never be persisted/joined.
func (tc TelemetryConfig) AnonymousFirst() bool {
	return tc.Identity == IdentityAnonymousFirst
}

// transformIP applies the configured IP-handling strategy to a request's
// RemoteAddr (which may be "host:port"). It returns the value safe to log or
// attach. Applied uniformly to request logging and event attachment.
func (tc TelemetryConfig) transformIP(remoteAddr string) string {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	switch tc.IPHandling {
	case IPDrop:
		return ""
	case IPFull:
		return remoteAddr
	case IPHash:
		if host == "" {
			return ""
		}
		sum := sha256.Sum256([]byte(tc.ipHashSalt + host))
		return "sha256:" + hex.EncodeToString(sum[:8])
	case IPTruncate:
		return truncateIP(host)
	}
	return remoteAddr
}

// truncateIP zeros the host portion of an address: the last octet of an IPv4
// address or the last 80 bits of an IPv6 address, preserving network-level
// grouping without identifying a single host.
func truncateIP(host string) string {
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		v4[3] = 0
		return v4.String()
	}
	v6 := ip.To16()
	if v6 == nil {
		return ""
	}
	for i := 6; i < 16; i++ {
		v6[i] = 0
	}
	return v6.String()
}
