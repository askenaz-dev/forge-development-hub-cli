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

// UnsupportedSchemaError is returned by Parse/Load when the catalog
// declares a schema_version this fdh release does not understand.
// Callers (notably `fdh validate-registry`) can switch on this to
// surface a "schema-version" finding rather than a YAML syntax error.
type UnsupportedSchemaError struct {
	Got       int
	Supported []int
}

func (e *UnsupportedSchemaError) Error() string {
	return fmt.Sprintf("schema_version %d not supported (this fdh supports %v)", e.Got, e.Supported)
}

// catalogRelPath is the path inside the hub clone where the catalog
// lives. The v2 source-of-truth is `hub/registry.yaml`; during the
// 60-day transition window the legacy mirror at
// `skills/registry.yaml` is also acceptable. Load tries v2 first,
// then falls back.
var catalogCandidatePaths = []string{
	filepath.Join("hub", "registry.yaml"),
	filepath.Join("skills", "registry.yaml"),
}

// Load resolves the cache dir, ensures a shallow clone of registryURL
// at the requested branch with sparse-checkout limited to the catalog
// paths, parses the YAML, and returns the result.
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
				log(fmt.Sprintf("fetch failed (using cached data): %v", err))
			}
		}
	} else if err == nil {
		return nil, &CorruptCacheError{CacheDir: cacheDir, Cause: fmt.Errorf("%s exists but is not a directory", gitDir)}
	} else {
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

	raw, catalogRel, err := readCatalog(cacheDir)
	if err != nil {
		return nil, err
	}
	reg, err := parse(raw, log)
	if err != nil {
		return nil, fmt.Errorf("hubregistry: parse %s: %w", catalogRel, err)
	}
	reg.LocalPath = cacheDir
	reg.HubCommit = head
	return reg, nil
}

// readCatalog finds the catalog file in the hub clone and returns its
// bytes plus a hub-relative display path. Prefers v2
// (`hub/registry.yaml`); falls back to v1 mirror
// (`skills/registry.yaml`).
func readCatalog(cacheDir string) ([]byte, string, error) {
	for _, rel := range catalogCandidatePaths {
		abs := filepath.Join(cacheDir, rel)
		if b, err := os.ReadFile(abs); err == nil {
			return b, filepath.ToSlash(rel), nil
		}
	}
	// Report against the v2 path for actionable error messages.
	return nil, "", fmt.Errorf("hubregistry: no catalog found at %s (tried: %s)",
		catalogCandidatePaths[0],
		strings.Join(catalogCandidatePaths, ", "))
}

// FetchComponent extends the sparse-checkout to include the
// component's source directory and returns the absolute on-disk path.
// Components are looked up by (name, kind).
func (r *Registry) FetchComponent(ctx context.Context, name, kind string) (string, error) {
	if !kindOK(kind) {
		return "", fmt.Errorf("hubregistry: unknown kind %q (want one of %s)", kind, strings.Join(AllKinds, ", "))
	}
	entry := r.ComponentByName(name, kind)
	if entry == nil {
		return "", fmt.Errorf("hubregistry: component %q (kind=%s) not in registry", name, kind)
	}
	if r.LocalPath == "" {
		return "", errors.New("hubregistry: registry has no LocalPath (was it loaded?)")
	}
	if err := addSparsePath(ctx, r.LocalPath, entry.Path); err != nil {
		return "", fmt.Errorf("hubregistry: extend sparse-checkout for %s/%s: %w", kind, name, err)
	}
	abs := filepath.Join(r.LocalPath, filepath.FromSlash(entry.Path))
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("hubregistry: component path %s not present after sparse-checkout: %w", abs, err)
	}
	return abs, nil
}

// FetchSkill is a back-compat shim for FetchComponent(..., "skill").
//
// Deprecated: use FetchComponent. Kept during the transition window.
func (r *Registry) FetchSkill(ctx context.Context, name string) (string, error) {
	return r.FetchComponent(ctx, name, KindSkill)
}

// RecoverFromCorruption deletes the cache directory so the next Load
// call performs a fresh clone. Safe to call multiple times.
func RecoverFromCorruption(cacheDir string) error {
	if cacheDir == "" {
		return errors.New("hubregistry: RecoverFromCorruption needs a cacheDir")
	}
	return os.RemoveAll(cacheDir)
}

// ComponentByName returns the entry matching (name, kind), or nil.
func (r *Registry) ComponentByName(name, kind string) *ComponentEntry {
	for i := range r.Components {
		if r.Components[i].Name == name && r.Components[i].Kind == kind {
			return &r.Components[i]
		}
	}
	return nil
}

// ComponentsByKind returns the slice of entries whose Kind matches.
// Always returns a non-nil slice (empty if no matches).
func (r *Registry) ComponentsByKind(kind string) []ComponentEntry {
	out := []ComponentEntry{}
	for _, c := range r.Components {
		if c.Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

// SkillByName returns the skill matching name as a *SkillEntry.
//
// Deprecated: use ComponentByName(name, KindSkill). Kept during the
// transition window.
func (r *Registry) SkillByName(name string) *SkillEntry {
	for i := range r.Skills {
		if r.Skills[i].Name == name {
			return &r.Skills[i]
		}
	}
	return nil
}

// Parse decodes catalog bytes into a *Registry, routing by
// schema_version (2 native or 1 legacy mirror). Populates the derived
// Skills view. Logger receives the v1→v2 deprecation warning; nil is
// allowed (warning discarded).
//
// Exposed so callers like `fdh validate-registry` can parse arbitrary
// bytes without going through the on-disk cache layout.
func Parse(raw []byte, log func(string)) (*Registry, error) {
	if log == nil {
		log = func(string) {}
	}
	return parse(raw, log)
}

// --- internals ---

// parse decodes the catalog bytes. Detects schema_version first and
// routes to the v2 or v1 path. Populates Registry.Skills as a derived
// view at the end.
func parse(raw []byte, log func(string)) (*Registry, error) {
	// Stage 1: detect schema_version with a permissive decode.
	var head struct {
		SchemaVersion int `yaml:"schema_version"`
	}
	if err := yaml.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("strict decode (header): %w", err)
	}

	switch head.SchemaVersion {
	case 2:
		var r Registry
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		dec.KnownFields(true)
		if err := dec.Decode(&r); err != nil {
			return nil, err
		}
		populateSkillsView(&r)
		return &r, nil

	case 1:
		// Legacy mirror: decode with v1 shape, normalize, warn.
		legacy := struct {
			SchemaVersion int          `yaml:"schema_version"`
			GeneratedAt   string       `yaml:"generated_at,omitempty"`
			Skills        []SkillEntry `yaml:"skills"`
		}{}
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		dec.KnownFields(true)
		if err := dec.Decode(&legacy); err != nil {
			return nil, fmt.Errorf("strict decode (v1): %w", err)
		}
		r := &Registry{
			SchemaVersion: 1,
			Components:    make([]ComponentEntry, 0, len(legacy.Skills)),
		}
		for _, s := range legacy.Skills {
			r.Components = append(r.Components, ComponentEntry{
				Name:            s.Name,
				Kind:            KindSkill,
				Path:            s.Path,
				Description:     s.Description,
				Default:         s.Default,
				MinFDHVersion:   s.MinFDHVersion,
				AgentsSupported: s.AgentsSupported,
				Version:         s.Version,
				Tags:            s.Tags,
			})
		}
		log("hubregistry: registry v1 mirror at skills/registry.yaml is deprecated; cleanup planned ~2026-07-22 — switch to hub/registry.yaml")
		populateSkillsView(r)
		return r, nil

	default:
		return nil, &UnsupportedSchemaError{Got: head.SchemaVersion, Supported: []int{1, 2}}
	}
}

// populateSkillsView fills Registry.Skills from Components (kind=skill).
// Called by parse so both v1 and v2 loads expose the derived view.
func populateSkillsView(r *Registry) {
	r.Skills = nil
	for _, c := range r.Components {
		if c.Kind != KindSkill {
			continue
		}
		r.Skills = append(r.Skills, SkillEntry{
			Name:            c.Name,
			Path:            c.Path,
			Default:         c.Default,
			AgentsSupported: c.AgentsSupported,
			Description:     c.Description,
			Version:         c.Version,
			MinFDHVersion:   c.MinFDHVersion,
			Tags:            c.Tags,
		})
	}
}

func systemGitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// sparseClone performs a shallow, branch-pinned, sparse clone. The
// initial sparse pattern covers BOTH v2 and v1 catalog paths so the
// post-clone read can find whichever exists.
func sparseClone(ctx context.Context, url, dest, branch string, log func(string)) error {
	log(fmt.Sprintf("cloning %s into %s", url, dest))
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
	if err := runGit(ctx, dest, "sparse-checkout", "set",
		"hub/registry.yaml",
		"hub/harnesses.yaml",
		"skills/registry.yaml",
	); err != nil {
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
	// Re-set the sparse-checkout pattern so caches created by older
	// fdh releases (which omitted hub/harnesses.yaml) materialize the
	// file on next refresh. Idempotent — git sparse-checkout set
	// replaces the pattern wholesale.
	if err := runGit(ctx, dest, "sparse-checkout", "set",
		"hub/registry.yaml",
		"hub/harnesses.yaml",
		"skills/registry.yaml",
	); err != nil {
		return err
	}
	return nil
}

// addSparsePath appends a path to the sparse-checkout set without
// dropping existing patterns.
func addSparsePath(ctx context.Context, dest, path string) error {
	clean := filepath.ToSlash(filepath.Clean(path))
	return runGit(ctx, dest, "sparse-checkout", "add", clean)
}

func fsck(ctx context.Context, dest string) error {
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
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
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
