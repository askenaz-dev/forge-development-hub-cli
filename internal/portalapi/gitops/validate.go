package gitops

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/forge/fdh/pkg/bundle"
	"github.com/forge/fdh/pkg/portability"
	"github.com/forge/fdh/pkg/scan"
)

// DefaultAgentIDs is the canonical adapter set used to cross-check a
// non-portable bundle's compatibility list during the portability lint. It
// mirrors the four agents the hub registry recognizes. The handler can pass a
// richer set sourced from the live adapter map; this is the safe default so the
// gitops package has no hard dependency on the CLI run context.
var DefaultAgentIDs = []string{"claude-code", "codex", "copilot", "opencode"}

// ImportMeta is the trusted, server-verified metadata for an import. owner_team
// and agents shape the registry entry; the bundle's own description is the
// source of truth for the entry description (mirrors the CLI).
type ImportMeta struct {
	// OwnerTeam populates the registry entry's owner_team (falls back to
	// "unassigned", mirroring registryEntryYAML).
	OwnerTeam string
	// Agents, when set, populates agents_supported; empty defaults per-kind
	// exactly like the CLI.
	Agents []string
}

// ValidateBundleDir runs the SAME validators the CLI runs in runKindShare,
// server-side, BEFORE any push — and returns a typed *ErrValidation naming the
// first failing gate. Order matches the CLI: load → validate → portability →
// scan. A scan that produces a blocking ("fail") verdict aborts; a scan that
// merely errors (cannot run) does NOT abort (mirrors the catalog's
// scan-never-aborts posture for read paths, but here a "fail" verdict is a hard
// stop because we are about to publish).
//
// It returns the loaded *bundle.Bundle on success so the caller can read the
// description and compatibility without re-loading.
func ValidateBundleDir(bundleDir string, knownAgentIDs []string) (*bundle.Bundle, error) {
	if len(knownAgentIDs) == 0 {
		knownAgentIDs = DefaultAgentIDs
	}

	b, err := bundle.Load(bundleDir)
	if err != nil {
		return nil, &ErrValidation{Check: "bundle", Detail: err.Error()}
	}
	if err := b.Validate(); err != nil {
		return nil, &ErrValidation{Check: "validate", Detail: err.Error()}
	}
	if findings := portability.Lint(b, portability.LintOptions{KnownAgentIDs: knownAgentIDs}); portability.HasErrors(findings) {
		return nil, &ErrValidation{Check: "portability", Detail: portability.Format(findings)}
	}
	status, err := scan.DirStatus(bundleDir)
	if err != nil {
		// The scan could not run — record nothing blocking, but surface a clear
		// validation error so the import is not silently published unscanned.
		return nil, &ErrValidation{Check: "scan", Detail: "security scan could not run: " + err.Error()}
	}
	if status == scan.StatusFail {
		return nil, &ErrValidation{Check: "scan", Detail: "security scan reported blocking findings (status=fail)"}
	}
	return b, nil
}

// bundleAgents resolves the agents_supported list for the registry entry. It
// prefers the explicit meta.Agents (trusted), else the bundle's own
// `compatibility` frontmatter (a non-portable skill may pin agents), else the
// per-kind default applied by registryEntryYAML (handled downstream when empty).
func bundleAgents(b *bundle.Bundle, meta ImportMeta) []string {
	if len(meta.Agents) > 0 {
		return meta.Agents
	}
	if len(b.SkillMD.Compatibility) > 0 {
		return append([]string(nil), b.SkillMD.Compatibility...)
	}
	return nil
}

// collectBundleFiles walks bundleDir and returns FileChanges placing every file
// under destPrefix (e.g. "skills/card-grid"), skipping dotfiles/dirs exactly
// like the CLI copyTree. Paths in the returned changes are repo-relative,
// forward-slash. This is the bundle half of the import commit.
func collectBundleFiles(bundleDir, destPrefix string) ([]FileChange, error) {
	var changes []FileChange
	err := filepath.WalkDir(bundleDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(bundleDir, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		// Skip dotfiles/dirs (mirrors copyTree).
		if strings.HasPrefix(filepath.Base(p), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		repoPath := destPrefix + "/" + filepath.ToSlash(rel)
		changes = append(changes, FileChange{Path: repoPath, Content: data})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("collect bundle files: %w", err)
	}
	return changes, nil
}
