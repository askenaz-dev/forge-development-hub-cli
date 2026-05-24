package hubregistry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// cacheKey returns a short, filesystem-safe digest of registryURL so
// each distinct URL gets its own clone under DefaultCacheDir. Twelve
// hex chars is enough collision space for the small number of hubs a
// developer is likely to point at.
func cacheKey(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:6])
}

// CorruptCacheError is returned by Load when the cache directory is
// present but `git fsck` fails. RecoverFromCorruption resolves it.
type CorruptCacheError struct {
	CacheDir string
	Cause    error
}

func (e *CorruptCacheError) Error() string {
	return fmt.Sprintf("hub cache at %s is corrupt: %v", e.CacheDir, e.Cause)
}

// Load resolves the cache dir, ensures a shallow clone of registryURL
// at the requested branch with sparse-checkout limited to
// `skills/registry.yaml`, parses the YAML, and returns the result.
//
// On corruption (`git fsck` failure), Load returns *CorruptCacheError
// and leaves the cache untouched. Callers should call
// RecoverFromCorruption() and retry.
func Load(ctx context.Context, registryURL string, opts LoadOptions) (*Registry, error) {
	if registryURL == "" {
		return nil, errors.New("hubregistry: registryURL is required")
	}

	cacheDir := opts.CacheDir
	if cacheDir == "" {
		base := DefaultCacheDir()
		if base == "" {
			return nil, errors.New("hubregistry: could not resolve a cache directory (set XDG_CACHE_HOME or HOME)")
		}
		// Derive a per-URL subdir so multiple registry URLs (or test
		// runs against ephemeral fixture hubs) don't share a clone.
		cacheDir = filepath.Join(base, cacheKey(registryURL))
	}

	branch := opts.Branch
	if branch == "" {
		branch = "main"
	}

	log := opts.Logger
	if log == nil {
		log = func(string) {}
	}

	if !systemGitAvailable() {
		return nil, errors.New("hubregistry: system `git` not on PATH (required for sparse-checkout)")
	}

	gitDir := filepath.Join(cacheDir, ".git")
	if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
		if !opts.SkipFetch {
			if err := fsck(ctx, cacheDir); err != nil {
				return nil, &CorruptCacheError{CacheDir: cacheDir, Cause: err}
			}
			if err := fetchAndCheckout(ctx, cacheDir, branch, log); err != nil {
				// Network failures are tolerated: fall through to a
				// cached read so a developer can still work offline.
				log(fmt.Sprintf("fetch failed (using cached data): %v", err))
			}
		}
	} else if err == nil {
		// `.git` exists at the path but isn't a directory. Corrupt.
		return nil, &CorruptCacheError{CacheDir: cacheDir, Cause: fmt.Errorf("%s exists but is not a directory", gitDir)}
	} else {
		// `.git` missing. If the cache directory itself exists and is
		// non-empty, something else owns this path — refuse to stomp
		// on it and flag corruption so the caller can recover.
		if nonEmpty, statErr := dirNonEmpty(cacheDir); statErr == nil && nonEmpty {
			return nil, &CorruptCacheError{CacheDir: cacheDir, Cause: fmt.Errorf("%s exists but contains no .git", cacheDir)}
		}
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return nil, fmt.Errorf("hubregistry: mkdir cache: %w", err)
		}
		if err := sparseClone(ctx, registryURL, cacheDir, branch, log); err != nil {
			return nil, fmt.Errorf("hubregistry: clone %s: %w", registryURL, err)
		}
	}

	head, err := headCommit(ctx, cacheDir)
	if err != nil {
		return nil, fmt.Errorf("hubregistry: read HEAD: %w", err)
	}

	registryPath := filepath.Join(cacheDir, "skills", "registry.yaml")
	raw, err := os.ReadFile(registryPath)
	if err != nil {
		return nil, fmt.Errorf("hubregistry: read %s: %w", registryPath, err)
	}
	reg, err := parse(raw)
	if err != nil {
		return nil, fmt.Errorf("hubregistry: parse registry.yaml: %w", err)
	}
	reg.LocalPath = cacheDir
	reg.HubCommit = head
	return reg, nil
}

// FetchSkill extends the sparse-checkout to include `skills/<name>/`
// and returns the absolute on-disk path of that directory.
//
// `name` is the entry's Name (kebab-case identifier); the actual path
// is resolved from the corresponding SkillEntry.Path so the registry
// is the source of truth.
func (r *Registry) FetchSkill(ctx context.Context, name string) (string, error) {
	entry := r.findByName(name)
	if entry == nil {
		return "", fmt.Errorf("hubregistry: skill %q not in registry", name)
	}
	if r.LocalPath == "" {
		return "", errors.New("hubregistry: registry has no LocalPath (was it loaded?)")
	}
	if err := addSparsePath(ctx, r.LocalPath, entry.Path); err != nil {
		return "", fmt.Errorf("hubregistry: extend sparse-checkout for %s: %w", name, err)
	}
	abs := filepath.Join(r.LocalPath, filepath.FromSlash(entry.Path))
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("hubregistry: skill path %s not present after sparse-checkout: %w", abs, err)
	}
	return abs, nil
}

// RecoverFromCorruption deletes the cache directory so the next Load
// call performs a fresh clone. Safe to call multiple times.
func RecoverFromCorruption(cacheDir string) error {
	if cacheDir == "" {
		return errors.New("hubregistry: RecoverFromCorruption needs a cacheDir")
	}
	return os.RemoveAll(cacheDir)
}

// findByName returns the SkillEntry with Name == name, or nil.
func (r *Registry) findByName(name string) *SkillEntry {
	for i := range r.Skills {
		if r.Skills[i].Name == name {
			return &r.Skills[i]
		}
	}
	return nil
}

// SkillByName is the exported lookup used by consumers (wizard,
// update) that already hold a Registry and just want one entry.
func (r *Registry) SkillByName(name string) *SkillEntry { return r.findByName(name) }

// --- internals (git shell-outs) ---

func parse(raw []byte) (*Registry, error) {
	var r Registry
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

func systemGitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// sparseClone performs a shallow, branch-pinned, sparse clone. The
// initial sparse pattern is `skills/registry.yaml` only; FetchSkill
// extends it on demand.
func sparseClone(ctx context.Context, url, dest, branch string, log func(string)) error {
	log(fmt.Sprintf("cloning %s into %s", url, dest))
	// 1. Bare init + remote add, so we can configure sparse before checkout.
	if err := runGit(ctx, "", "init", "--initial-branch", branch, dest); err != nil {
		return fmt.Errorf("git init: %w", err)
	}
	if err := runGit(ctx, dest, "remote", "add", "origin", url); err != nil {
		return fmt.Errorf("git remote add: %w", err)
	}
	if err := runGit(ctx, dest, "config", "core.sparseCheckout", "true"); err != nil {
		return fmt.Errorf("git config sparseCheckout: %w", err)
	}
	if err := runGit(ctx, dest, "sparse-checkout", "init", "--no-cone"); err != nil {
		return fmt.Errorf("sparse-checkout init: %w", err)
	}
	if err := runGit(ctx, dest, "sparse-checkout", "set", "skills/registry.yaml"); err != nil {
		return fmt.Errorf("sparse-checkout set: %w", err)
	}
	if err := runGit(ctx, dest, "fetch", "--depth", "1", "origin", branch); err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	if err := runGit(ctx, dest, "checkout", "FETCH_HEAD"); err != nil {
		return fmt.Errorf("checkout: %w", err)
	}
	return nil
}

func fetchAndCheckout(ctx context.Context, dest, branch string, log func(string)) error {
	log(fmt.Sprintf("fetching %s from origin", branch))
	if err := runGit(ctx, dest, "fetch", "--depth", "1", "origin", branch); err != nil {
		return err
	}
	if err := runGit(ctx, dest, "reset", "--hard", "FETCH_HEAD"); err != nil {
		return err
	}
	return nil
}

// addSparsePath appends a path to the sparse-checkout set without
// dropping existing patterns. Git's `sparse-checkout add` does
// exactly that. `path` is forward-slash hub-relative
// (e.g. "skills/design-system").
func addSparsePath(ctx context.Context, dest, path string) error {
	clean := filepath.ToSlash(filepath.Clean(path))
	return runGit(ctx, dest, "sparse-checkout", "add", clean)
}

func fsck(ctx context.Context, dest string) error {
	// `--no-progress` keeps the output quiet; we only care about exit
	// status. We don't pass `--full` (which would walk every object)
	// because the cache is shallow and big-O matters on slow disks.
	out, err := runGitCapture(ctx, dest, "fsck", "--no-progress")
	if err != nil {
		return fmt.Errorf("git fsck: %w (%s)", err, strings.TrimSpace(out))
	}
	return nil
}

func headCommit(ctx context.Context, dest string) (string, error) {
	out, err := runGitCapture(ctx, dest, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// dirNonEmpty reports whether the directory at path exists and has at
// least one entry. Returns (false, error) only when the path doesn't
// exist or can't be read.
func dirNonEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}

func runGit(ctx context.Context, cwd string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", // never prompt for credentials in a CLI context
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func runGitCapture(ctx context.Context, cwd string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	return string(out), err
}
