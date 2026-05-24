package adapters

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SkillAdapter is the per-ecosystem strategy for materializing a
// hub skill onto disk so the target AI agent reads it. There is
// one implementation per agent (Claude Code, Codex, Copilot,
// OpenCode); the consumer iterates `for skill in selected: for
// adapter in selected: adapter.Install(...)`.
//
// Why an interface (vs. one big switch): each ecosystem has
// different layout rules — Claude/Codex copy a whole directory
// while Copilot/OpenCode flatten the SKILL.md into a single
// prompt file. Keeping each transformation in its own type lets
// tests target the boundary cleanly.
type SkillAdapter interface {
	// Agent returns the agent id this adapter targets
	// ("claude-code", "codex", "copilot", "opencode"). Used to
	// match against `agents_supported` in registry.yaml.
	Agent() string

	// TargetPath returns the absolute on-disk path the skill's
	// materialized form will live at. For directory-based agents
	// (Claude, Codex) this is the directory itself; for flat
	// agents (Copilot, OpenCode) it is the prompt-file path.
	//
	// projectRoot is the path of the consumer's repo (when scope
	// is "project"); homeDir is the user's home directory.
	TargetPath(skillName, projectRoot, homeDir string, scope Scope) (string, error)

	// Install materializes srcDir (the hub's `skills/<name>/`
	// directory) into the agent's convention and returns an
	// InstallResult including the .skill-version marker path
	// and the recorded content hash.
	Install(srcDir string, opts InstallOpts) (InstallResult, error)

	// SupportsSubresources is true when the agent reads multiple
	// files (Claude/Codex: SKILL.md + references/) and false when
	// only the SKILL.md body is reachable (Copilot/OpenCode).
	// The wizard uses this to warn when a multi-file skill is
	// being installed to a flat-only agent.
	SupportsSubresources() bool
}

// InstallOpts configures one adapter call.
type InstallOpts struct {
	// SkillName is the kebab-case identifier from registry.yaml.
	SkillName string

	// ProjectRoot is the absolute path of the consumer repo,
	// required when Scope == ScopeProject.
	ProjectRoot string

	// HomeDir is the user's home directory.
	HomeDir string

	// Scope picks user vs project install target.
	Scope Scope

	// HubVersion is the catalog version recorded in
	// `.skill-version` (e.g. registry.yaml `version` field).
	HubVersion string

	// HubCommit is the SHA of the hub HEAD at install time.
	HubCommit string

	// InstalledByFDH is the semver of the CLI doing the install.
	InstalledByFDH string

	// Overwrite controls whether existing files are replaced. The
	// wizard sets this false (init); `fdh update` sets it true.
	Overwrite bool

	// DryRun makes Install compute hashes + plan but skip every
	// filesystem write. The returned InstallResult is populated as
	// if the install had happened.
	DryRun bool
}

// InstallResult is what one adapter call produced.
type InstallResult struct {
	// Agent that received the install. Same as adapter.Agent().
	Agent string

	// SkillName installed.
	SkillName string

	// TargetPath where the materialized form lives.
	TargetPath string

	// MarkerPath is the absolute path of the `.skill-version`
	// (or `.skill-version-<name>` for flat agents) file.
	MarkerPath string

	// ContentHash is the canonical SHA-256 of the installed
	// content, computed with LF-normalised line endings.
	ContentHash string

	// Skipped is true when Install detected the target was
	// already up-to-date (same hash) and chose not to write.
	Skipped bool

	// Warnings is any non-fatal messages worth surfacing
	// (e.g. "skill had references/ folder; not portable to
	// flat agent <X>, dropped").
	Warnings []string

	// FilesWritten lists the relative paths (from TargetPath)
	// of files the adapter wrote. Empty when Skipped or DryRun.
	FilesWritten []string
}

// SkillVersionMarker is the YAML shape recorded next to each
// installed skill. The format is shared across all adapters so
// `fdh update` can deserialize it uniformly.
//
// `content_hash` is recomputed at update time and compared to
// detect local edits (drift detection).
type SkillVersionMarker struct {
	Name           string    `yaml:"name"`
	HubVersion     string    `yaml:"hub_version"`
	HubCommit      string    `yaml:"hub_commit"`
	InstalledAt    time.Time `yaml:"installed_at"`
	InstalledByFDH string    `yaml:"installed_by_fdh"`
	ContentHash    string    `yaml:"content_hash"`
	// Agent records which adapter wrote the marker. Useful for
	// `fdh update` when scanning a directory that several agents
	// might share.
	Agent string `yaml:"agent"`
}

// ComputeContentHash returns the SHA-256 of every regular file
// under dir, LF-normalised before hashing.
//
// The hash is order-stable: file paths are sorted lexicographically
// (forward-slash form, relative to dir) and concatenated with their
// content so a re-install on a different OS produces the same hash
// despite git's CRLF rewrites.
//
// Errors propagate the underlying os error.
func ComputeContentHash(dir string) (string, error) {
	type entry struct {
		rel  string
		body []byte
	}
	var files []entry
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		// Skip the marker file itself — it changes on every
		// install (installed_at, hub_commit) and would make
		// the hash non-deterministic.
		if isMarker(d.Name()) {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files = append(files, entry{
			rel:  filepath.ToSlash(rel),
			body: normaliseLF(body),
		})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })

	h := sha256.New()
	for _, f := range files {
		// path\0len:body\0 keeps boundaries unambiguous so two
		// concatenations don't collide ("ab"+"c" vs "a"+"bc").
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", f.rel, len(f.body))
		_, _ = h.Write(f.body)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// isMarker reports whether name is one of the per-skill marker
// files written by adapters. Excluded from content-hash so
// re-installs are idempotent.
func isMarker(name string) bool {
	return name == ".skill-version" || strings.HasPrefix(name, ".skill-version-")
}

// normaliseLF rewrites CRLF and lone CR into LF so the hash is
// stable across Windows checkouts (where git.autocrlf may have
// rewritten the file on disk).
func normaliseLF(in []byte) []byte {
	if len(in) == 0 {
		return in
	}
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); i++ {
		c := in[i]
		switch c {
		case '\r':
			out = append(out, '\n')
			if i+1 < len(in) && in[i+1] == '\n' {
				i++ // consume the LF half of a CRLF
			}
		default:
			out = append(out, c)
		}
	}
	return out
}

// copyTree copies srcDir into dstDir recursively, preserving
// executable bits where set. Files inside `.git` (if any) are
// skipped. Returns the list of relative paths actually written
// (forward-slash, relative to dstDir).
func copyTree(srcDir, dstDir string, overwrite bool, dryRun bool) ([]string, error) {
	var written []string
	err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// Skip git metadata that may sneak in via sparse-checkout.
		if strings.HasPrefix(filepath.ToSlash(rel), ".git/") || rel == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dest := filepath.Join(dstDir, rel)
		if d.IsDir() {
			if dryRun {
				return nil
			}
			return os.MkdirAll(dest, 0o755)
		}
		if !overwrite {
			if _, err := os.Stat(dest); err == nil {
				// Honor overwrite=false by skipping existing files;
				// the install logic's "skipped" pathway short-circuits
				// before reaching here, so this is a defensive guard.
				return nil
			}
		}
		if dryRun {
			written = append(written, filepath.ToSlash(rel))
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if info, err := d.Info(); err == nil && info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		if err := os.WriteFile(dest, body, mode); err != nil {
			return err
		}
		written = append(written, filepath.ToSlash(rel))
		return nil
	})
	return written, err
}

// hasSubresources reports whether srcDir contains anything other
// than SKILL.md at the top level. Used by flat adapters to decide
// whether to emit a "lossy install" warning.
func hasSubresources(srcDir string) bool {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			return true
		}
		if e.Name() != "SKILL.md" {
			return true
		}
	}
	return false
}

// readSkillMD returns the body of srcDir/SKILL.md or a descriptive
// error if the file is missing.
func readSkillMD(srcDir string) ([]byte, error) {
	body, err := os.ReadFile(filepath.Join(srcDir, "SKILL.md"))
	if err != nil {
		return nil, fmt.Errorf("read SKILL.md: %w", err)
	}
	return body, nil
}

// writeFileAtomic writes body to path via a temp file + rename so a
// partial write never leaves a half-baked file on disk.
func writeFileAtomic(path string, body []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".fdh-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, strings.NewReader(string(body))); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}
