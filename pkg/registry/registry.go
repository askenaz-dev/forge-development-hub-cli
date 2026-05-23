// Package registry defines the Registry interface and its GitRegistry
// implementation.
//
// Call sites depend on the interface, never on a concrete implementation,
// so later changes can introduce HTTPRegistry without touching CLI code.
package registry

import (
	"context"
	"strings"
)

// Registry is the abstraction the CLI talks to. The GitRegistry implementation
// reads from a local Git clone of the registry repository; a future
// HTTPRegistry would replace it without any CLI-level change.
type Registry interface {
	// Index returns the registry's catalog, refreshing it if necessary.
	Index(ctx context.Context) (Index, error)

	// Manifest returns the per-skill manifest for namespace/name.
	Manifest(ctx context.Context, namespace, name string) (Manifest, error)

	// FetchBundle returns a BundlePath pointing at an extracted, hash-
	// verified bundle directory. The caller is responsible for cleanup
	// via BundlePath.Cleanup when finished.
	FetchBundle(ctx context.Context, namespace, name, version string) (BundlePath, error)

	// Search returns skills whose name, namespace, description, or tags
	// match the query string. Match semantics are case-insensitive
	// substring across all fields.
	Search(ctx context.Context, query string) ([]SkillSummary, error)

	// CheckConsistency cross-references the catalog with per-skill manifests
	// and reports any inconsistencies (used by `doctor`).
	CheckConsistency(ctx context.Context) []ConsistencyIssue

	// Source returns a human-readable description of where the registry
	// data comes from (URL, local path, etc.) for diagnostic output.
	Source() string
}

// BundlePath holds the result of FetchBundle: the path to the extracted
// bundle directory and a cleanup function the caller must invoke.
type BundlePath struct {
	// Path is the absolute filesystem path of the extracted bundle/ directory.
	Path string

	// Hash is the verified canonical SHA-256 hex digest of the bundle.
	Hash string

	cleanup func() error
}

// Cleanup removes the extracted bundle directory. Safe to call multiple times.
func (b BundlePath) Cleanup() error {
	if b.cleanup == nil {
		return nil
	}
	err := b.cleanup()
	b.cleanup = nil
	return err
}

// ConsistencyIssue is a single problem found by CheckConsistency.
type ConsistencyIssue struct {
	Skill    string // namespace/name
	Severity string // "warning" or "error"
	Message  string
}

// matchQuery tests whether s contains query, case-insensitive.
func matchQuery(s, query string) bool {
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(s), strings.ToLower(query))
}
