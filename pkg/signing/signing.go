// Package signing verifies component signatures at install time
// (capability bundle-signing).
//
// To honor the CLI's zero-new-Go-dependencies constraint, verification shells
// out to the `cosign` binary when it is available (mirroring the system-git
// fallback in pkg/registry). The signed artifact is the version's canonical
// content_hash; the signature travels in the manifest's reserved `signature`
// field (a self-contained cosign bundle), so no new wire-protocol endpoint is
// introduced.
package signing

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Policy controls how the install path treats signatures.
type Policy int

const (
	// PolicyDefault verifies when a signature is present and cosign is
	// available; absence of either is tolerated (unsigned mirrors install).
	PolicyDefault Policy = iota
	// PolicyRequire refuses to install unless a verifiable signature is present.
	PolicyRequire
)

// PolicyFromEnv returns PolicyRequire when FDH_REQUIRE_SIGNATURES is truthy.
func PolicyFromEnv() Policy {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FDH_REQUIRE_SIGNATURES"))) {
	case "1", "true", "yes", "on":
		return PolicyRequire
	default:
		return PolicyDefault
	}
}

// Verifier checks a signature over a content hash and returns the signer
// identity on success. Implemented by CosignVerifier; stubbed in tests.
type Verifier interface {
	// Available reports whether the verifier can run (e.g. cosign on PATH).
	Available() bool
	// Verify checks that signature attests contentHash and returns the signer
	// identity (e.g. the certificate subject) on success.
	Verify(ctx context.Context, contentHash, signature string) (signer string, err error)
}

// Check applies policy to a (signature, verifier) pair. It returns the signer
// identity to record in provenance (possibly empty) or an error that MUST abort
// the install. It performs no I/O of its own beyond delegating to the verifier,
// so it is fully unit-testable with a stub Verifier.
func Check(ctx context.Context, policy Policy, contentHash, signature string, v Verifier) (signer string, err error) {
	if signature == "" {
		if policy == PolicyRequire {
			return "", fmt.Errorf("signature required by policy but the registry published none for this version")
		}
		return "", nil // unsigned source; allowed under the default policy
	}
	if v == nil || !v.Available() {
		if policy == PolicyRequire {
			return "", fmt.Errorf("signature present but cosign is not available to verify it (required by policy)")
		}
		return "", nil // cannot verify without cosign; default policy installs
	}
	signer, err = v.Verify(ctx, contentHash, signature)
	if err != nil {
		return "", fmt.Errorf("signature verification failed: %w", err)
	}
	return signer, nil
}

// CosignVerifier verifies via the cosign binary. For keyless verification it
// requires the expected certificate identity + OIDC issuer (the CI workflow
// identity); for key-based verification it uses KeyPath.
type CosignVerifier struct {
	// KeyPath, when set, selects key-based verification (`--key`).
	KeyPath string
	// CertIdentityRegexp + CertOIDCIssuer select keyless verification.
	CertIdentityRegexp string
	CertOIDCIssuer     string
}

// CosignVerifierFromEnv reads the verifier configuration from the environment.
func CosignVerifierFromEnv() CosignVerifier {
	return CosignVerifier{
		KeyPath:            os.Getenv("FDH_COSIGN_KEY"),
		CertIdentityRegexp: os.Getenv("FDH_COSIGN_IDENTITY"),
		CertOIDCIssuer:     os.Getenv("FDH_COSIGN_ISSUER"),
	}
}

// Available reports whether the cosign binary is on PATH.
func (c CosignVerifier) Available() bool {
	_, err := exec.LookPath("cosign")
	return err == nil
}

// Verify writes the content hash and the signature bundle to temp files and
// runs `cosign verify-blob`, returning the signer identity on success.
func (c CosignVerifier) Verify(ctx context.Context, contentHash, signature string) (string, error) {
	tmp, err := os.MkdirTemp("", "fdh-cosign-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	blob := filepath.Join(tmp, "content-hash")
	if err := os.WriteFile(blob, []byte(contentHash), 0o600); err != nil {
		return "", err
	}
	bundleFile := filepath.Join(tmp, "cosign.bundle")
	if err := os.WriteFile(bundleFile, []byte(signature), 0o600); err != nil {
		return "", err
	}

	args := []string{"verify-blob", "--bundle", bundleFile}
	switch {
	case c.KeyPath != "":
		args = append(args, "--key", c.KeyPath)
	case c.CertIdentityRegexp != "" && c.CertOIDCIssuer != "":
		args = append(args,
			"--certificate-identity-regexp", c.CertIdentityRegexp,
			"--certificate-oidc-issuer", c.CertOIDCIssuer,
		)
	default:
		return "", fmt.Errorf("cosign verification needs FDH_COSIGN_KEY, or FDH_COSIGN_IDENTITY + FDH_COSIGN_ISSUER")
	}
	args = append(args, blob)

	out, err := exec.CommandContext(ctx, "cosign", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("cosign verify-blob: %v: %s", err, strings.TrimSpace(string(out)))
	}
	signer := c.CertIdentityRegexp
	if c.KeyPath != "" {
		signer = "key:" + filepath.Base(c.KeyPath)
	}
	return signer, nil
}
