package registry

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"gopkg.in/yaml.v3"

	"github.com/forge/fdh/pkg/bundle"
)

// GitRegistry is a Registry implementation backed by a local Git clone
// of the registry repository. Reads happen against the clone; the clone
// is refreshed lazily before Index/Manifest/Search operations.
type GitRegistry struct {
	// LocalPath is the absolute path to the working tree of the registry clone.
	LocalPath string

	// RemoteURL is the canonical remote URL. Empty if the clone has no
	// remote (e.g. a developer pre-populated the directory by hand for a
	// pilot or air-gapped run).
	RemoteURL string

	// Branch is the registry branch to track. Empty means "main".
	Branch string

	// SkipFetch disables the lazy refresh and forces reads from whatever
	// is already on disk. Used by tests and by callers that want to bound
	// network I/O explicitly.
	SkipFetch bool

	// Logger receives one-line operational messages. nil discards them.
	Logger func(line string)
}

// Source returns a human-readable description of the registry.
func (g *GitRegistry) Source() string {
	if g.RemoteURL != "" {
		return fmt.Sprintf("git:%s (clone at %s)", g.RemoteURL, g.LocalPath)
	}
	return fmt.Sprintf("git:%s", g.LocalPath)
}

// ensureClone makes sure LocalPath contains a usable working tree. The
// caller is responsible for the lazy-refresh decision; this method only
// guarantees the directory exists and contains an index.json.
func (g *GitRegistry) ensureClone(ctx context.Context) error {
	if g.LocalPath == "" {
		return fmt.Errorf("GitRegistry: LocalPath is empty")
	}
	if info, err := os.Stat(g.LocalPath); err == nil && info.IsDir() {
		return nil
	}
	if g.RemoteURL == "" {
		// Cannot clone without a remote. The intent for an air-gapped
		// install is to populate LocalPath out-of-band; we surface a
		// clear, code-3 error so the CLI knows what to print.
		return RegistryUnreachable{Detail: fmt.Sprintf("local clone missing at %s and no remote configured", g.LocalPath)}
	}
	g.log("cloning %s into %s", g.RemoteURL, g.LocalPath)
	_, err := gogit.PlainCloneContext(ctx, g.LocalPath, false, &gogit.CloneOptions{
		URL:           g.RemoteURL,
		ReferenceName: g.branchRef(),
		SingleBranch:  true,
	})
	if err != nil {
		// On auth-style failures, the design promises a system-`git` fallback.
		if isAuthError(err) && systemGitAvailable() {
			g.log("go-git clone failed with auth error; falling back to system git")
			if errFallback := systemGitClone(ctx, g.RemoteURL, g.LocalPath, g.branchName()); errFallback == nil {
				return nil
			}
		}
		return RegistryUnreachable{Detail: fmt.Sprintf("clone failed: %v", err)}
	}
	return nil
}

func (g *GitRegistry) branchName() string {
	if g.Branch == "" {
		return "main"
	}
	return g.Branch
}

func (g *GitRegistry) branchRef() plumbing.ReferenceName {
	return plumbing.NewBranchReferenceName(g.branchName())
}

// refresh performs a git fetch against the configured branch and resets the
// working tree to its tip. Failures are non-fatal — we log and continue,
// using whatever is already on disk (cached read fallback per spec).
//
// The shell-out fallback (systemGitFetch + systemGitCheckout) is used when
// go-git returns an auth error and the system git binary is available; this
// covers corporate-network configurations where the system credential
// helper succeeds but go-git's auth flow does not.
func (g *GitRegistry) refresh(ctx context.Context) {
	if g.SkipFetch {
		return
	}
	if g.RemoteURL == "" {
		// Local-only registry; nothing to refresh.
		return
	}
	repo, err := gogit.PlainOpen(g.LocalPath)
	if err != nil {
		g.log("cannot open clone for refresh: %v (using cached data)", err)
		return
	}

	branch := g.branchName()
	fetchOpts := &gogit.FetchOptions{
		Force: true,
		RefSpecs: []config.RefSpec{
			config.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branch, branch)),
		},
	}
	err = repo.FetchContext(ctx, fetchOpts)
	switch {
	case err == nil, errors.Is(err, gogit.NoErrAlreadyUpToDate):
		// Fetch succeeded (or nothing to fetch) — fall through to checkout.
	case isAuthError(err) && systemGitAvailable():
		g.log("go-git fetch failed with auth error; trying system git")
		if errFallback := systemGitFetch(ctx, g.LocalPath); errFallback != nil {
			g.log("system git fetch also failed: %v (using cached data)", errFallback)
			return
		}
		// Re-open the repo to pick up the fetched refs.
		if reopened, err2 := gogit.PlainOpen(g.LocalPath); err2 == nil {
			repo = reopened
		}
	default:
		g.log("fetch failed: %v (using cached data)", err)
		return
	}

	// Advance the working tree to the remote branch tip.
	if err := g.checkoutRemoteHead(ctx, repo, branch); err != nil {
		g.log("checkout failed: %v (using cached data)", err)
	}
}

// checkoutRemoteHead resets the working tree to refs/remotes/origin/<branch>.
// Falls back to the system git binary if go-git's worktree manipulation
// fails (the same auth-class failure mode applies in some corporate envs).
func (g *GitRegistry) checkoutRemoteHead(ctx context.Context, repo *gogit.Repository, branch string) error {
	remoteRef, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", branch), true)
	if err != nil {
		return fmt.Errorf("resolve origin/%s: %w", branch, err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		// No worktree (bare clone) is fine; reads still work from objects.
		return nil //nolint:nilerr // bare clone is a supported configuration
	}
	checkoutErr := wt.Checkout(&gogit.CheckoutOptions{
		Hash:  remoteRef.Hash(),
		Force: true,
	})
	if checkoutErr == nil {
		return nil
	}
	// Fallback to system git checkout for stubborn cases.
	if systemGitAvailable() {
		if err := systemGitHardReset(ctx, g.LocalPath, "origin/"+branch); err == nil {
			return nil
		}
	}
	return checkoutErr
}

// Index implements Registry.Index.
//
// Resolution order, in priority:
//
//  1. <LocalPath>/index.json — the v1 wire shape produced by the portal
//     (or by tests via internal/testutil.BuildRegistry). Strict JSON.
//  2. <LocalPath>/hub/registry.yaml — the v2 source-of-truth catalog the
//     hub repo edits directly. Parsed and synthesized into an Index so
//     callers that don't (yet) talk to a portal can still read components
//     from a direct git clone of the hub.
//
// (2) is the path that unblocks `fdh doctor`, `fdh search`, and
// `fdh install` against a registry URL that points at the hub git repo
// without an index.json published alongside (the common case for
// pre-portal installs and air-gapped mirrors of the hub). It returns
// the v2 Components view; normalize() then projects Skills for the
// derived view callers may use.
//
// When index.json is present, its content wins — that path stays the
// fastest and most stable (the portal serializes it). YAML fallback is
// only invoked when index.json is absent.
func (g *GitRegistry) Index(ctx context.Context) (Index, error) {
	if err := g.ensureClone(ctx); err != nil {
		return Index{}, err
	}
	g.refresh(ctx)

	idxPath := filepath.Join(g.LocalPath, "index.json")
	if _, err := os.Stat(idxPath); err == nil {
		return readIndex(idxPath)
	} else if !os.IsNotExist(err) {
		return Index{}, fmt.Errorf("stat %s: %w", idxPath, err)
	}

	// index.json absent — try the hub v2 catalog.
	yamlPath := filepath.Join(g.LocalPath, "hub", "registry.yaml")
	if _, err := os.Stat(yamlPath); err == nil {
		return readHubCatalog(yamlPath)
	} else if !os.IsNotExist(err) {
		return Index{}, fmt.Errorf("stat %s: %w", yamlPath, err)
	}

	return Index{}, fmt.Errorf(
		"no catalog found in %s (looked for index.json and hub/registry.yaml)",
		g.LocalPath,
	)
}

// Sync ensures LocalPath holds a working tree of the remote at the tracked
// branch, WITHOUT requiring an index.json. Callers that read repo content
// directly from the working tree (e.g. the portal reading hub/registry.yaml +
// the four primitive directories) use this to populate the checkout before
// reading it.
//
// Unlike ensureClone (which assumes any pre-existing directory is already a
// clone), Sync detects an empty/non-repo directory — the common case for a
// freshly-mounted emptyDir volume — and clones into it.
func (g *GitRegistry) Sync(ctx context.Context) error {
	if g.LocalPath == "" {
		return fmt.Errorf("GitRegistry: LocalPath is empty")
	}
	// Already a git repo → just refresh to the branch tip.
	if _, err := gogit.PlainOpen(g.LocalPath); err == nil {
		g.refresh(ctx)
		return nil
	}
	// Not a repo yet (missing or empty mount). Clone into it.
	if g.RemoteURL == "" {
		return RegistryUnreachable{Detail: fmt.Sprintf("no git repo at %s and no remote configured", g.LocalPath)}
	}
	g.log("cloning %s into %s", g.RemoteURL, g.LocalPath)
	_, err := gogit.PlainCloneContext(ctx, g.LocalPath, false, &gogit.CloneOptions{
		URL:           g.RemoteURL,
		ReferenceName: g.branchRef(),
		SingleBranch:  true,
	})
	if err != nil {
		if isAuthError(err) && systemGitAvailable() {
			g.log("go-git clone failed with auth error; falling back to system git")
			if errFb := systemGitClone(ctx, g.RemoteURL, g.LocalPath, g.branchName()); errFb == nil {
				return nil
			}
		}
		return RegistryUnreachable{Detail: fmt.Sprintf("clone failed: %v", err)}
	}
	return nil
}

// Manifest implements Registry.Manifest.
func (g *GitRegistry) Manifest(ctx context.Context, namespace, name string) (Manifest, error) {
	if err := g.ensureClone(ctx); err != nil {
		return Manifest{}, err
	}
	g.refresh(ctx)
	p := filepath.Join(g.LocalPath, "skills", namespace, name, "manifest.json")
	return readManifest(p)
}

// FetchBundle implements Registry.FetchBundle.
func (g *GitRegistry) FetchBundle(ctx context.Context, namespace, name, version string) (BundlePath, error) {
	if err := g.ensureClone(ctx); err != nil {
		return BundlePath{}, err
	}
	g.refresh(ctx)
	versionDir := filepath.Join(g.LocalPath, "skills", namespace, name, "versions", version)
	bundleTar := filepath.Join(versionDir, "bundle.tar.gz")
	sumFile := filepath.Join(versionDir, "bundle.sha256")

	if _, err := os.Stat(bundleTar); err != nil {
		return BundlePath{}, fmt.Errorf("bundle.tar.gz missing: %w", err)
	}
	if _, err := os.Stat(sumFile); err != nil {
		return BundlePath{}, fmt.Errorf("bundle.sha256 missing: %w", err)
	}

	// Extract into a temp dir.
	tmp, err := os.MkdirTemp("", "forge-bundle-*")
	if err != nil {
		return BundlePath{}, fmt.Errorf("mkdtemp: %w", err)
	}
	cleanup := func() error { return os.RemoveAll(tmp) }

	if err := extractTarGz(bundleTar, tmp); err != nil {
		_ = cleanup()
		return BundlePath{}, fmt.Errorf("extract bundle: %w", err)
	}

	// The tarball is expected to contain a single top-level directory
	// (the bundle/ folder produced by publish). Locate it and rename to
	// the skill's name so bundle.Validate sees a directory that matches
	// the SKILL.md frontmatter.
	extractedBundle, err := locateBundleDir(tmp)
	if err != nil {
		_ = cleanup()
		return BundlePath{}, err
	}
	renamed := filepath.Join(tmp, name)
	if extractedBundle != renamed {
		if err := os.Rename(extractedBundle, renamed); err != nil {
			_ = cleanup()
			return BundlePath{}, fmt.Errorf("rename extracted bundle: %w", err)
		}
		extractedBundle = renamed
	}

	// Compute canonical hash and compare with bundle.sha256.
	loaded, err := bundle.Load(extractedBundle)
	if err != nil {
		_ = cleanup()
		return BundlePath{}, fmt.Errorf("load extracted bundle: %w", err)
	}
	got, err := loaded.Hash()
	if err != nil {
		_ = cleanup()
		return BundlePath{}, fmt.Errorf("hash extracted bundle: %w", err)
	}
	sumBytes, err := os.ReadFile(sumFile)
	if err != nil {
		_ = cleanup()
		return BundlePath{}, fmt.Errorf("read sha256: %w", err)
	}
	expected := strings.TrimSpace(strings.Fields(string(sumBytes))[0])
	if got != expected {
		_ = cleanup()
		return BundlePath{}, HashMismatch{Expected: expected, Got: got}
	}

	return BundlePath{Path: extractedBundle, Hash: expected, cleanup: cleanup}, nil
}

// Search implements Registry.Search.
func (g *GitRegistry) Search(ctx context.Context, query string) ([]SkillSummary, error) {
	idx, err := g.Index(ctx)
	if err != nil {
		return nil, err
	}
	var out []SkillSummary
	for _, e := range idx.Skills {
		blob := strings.Join([]string{e.Namespace, e.Name, e.Description, strings.Join(e.Tags, " ")}, " ")
		if !matchQuery(blob, query) {
			continue
		}
		out = append(out, e.toSummary())
	}
	return out, nil
}

// CheckConsistency cross-references each index entry with the corresponding
// per-skill manifest and reports drift.
func (g *GitRegistry) CheckConsistency(ctx context.Context) []ConsistencyIssue {
	var issues []ConsistencyIssue
	idx, err := g.Index(ctx)
	if err != nil {
		return []ConsistencyIssue{{Severity: "error", Message: err.Error()}}
	}
	for _, e := range idx.Skills {
		m, err := g.Manifest(ctx, e.Namespace, e.Name)
		if err != nil {
			issues = append(issues, ConsistencyIssue{
				Skill:    e.Namespace + "/" + e.Name,
				Severity: "error",
				Message:  fmt.Sprintf("manifest unreadable: %v", err),
			})
			continue
		}
		if m.Latest != e.LatestVersion {
			issues = append(issues, ConsistencyIssue{
				Skill:    e.Namespace + "/" + e.Name,
				Severity: "warning",
				Message:  fmt.Sprintf("index latest=%s but manifest latest=%s", e.LatestVersion, m.Latest),
			})
		}
		if v := m.FindVersion(m.Latest); v != nil {
			if v.ContentHash != e.LatestHash {
				issues = append(issues, ConsistencyIssue{
					Skill:    e.Namespace + "/" + e.Name,
					Severity: "warning",
					Message:  fmt.Sprintf("index hash %s != manifest hash %s for latest=%s", e.LatestHash, v.ContentHash, m.Latest),
				})
			}
		}
	}
	return issues
}

func (g *GitRegistry) log(format string, args ...any) {
	if g.Logger == nil {
		return
	}
	g.Logger(fmt.Sprintf(format, args...))
}

// --- helpers ---

func readIndex(path string) (Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Index{}, fmt.Errorf("read %s: %w", path, err)
	}
	var idx Index
	if err := unmarshalStrict(data, &idx); err != nil {
		return Index{}, fmt.Errorf("parse %s: %w", path, err)
	}
	idx.normalize()
	return idx, nil
}

// hubCatalog mirrors the v2 schema of <hub-clone>/hub/registry.yaml.
// Kept private here (and duplicated in internal/portalapi/wire_handlers.go)
// so this package stays import-cycle-clean and free of dependencies on
// the portal layer.
type hubCatalog struct {
	SchemaVersion int            `yaml:"schema_version"`
	HubVersion    string         `yaml:"hub_version"`
	Components    []hubComponent `yaml:"components"`
}

// hubComponent is one entry under hubCatalog.Components.
type hubComponent struct {
	Name        string   `yaml:"name"`
	Kind        string   `yaml:"kind"`
	Description string   `yaml:"description"`
	OwnerTeam   string   `yaml:"owner_team"`
	Tags        []string `yaml:"tags,omitempty"`
	Default     bool     `yaml:"default,omitempty"`
	MinFDHVer   string   `yaml:"min_fdh_version,omitempty"`
	AgentsSup   []string `yaml:"agents_supported,omitempty"`
	Path        string   `yaml:"path"`
}

// readHubCatalog parses hub/registry.yaml and synthesizes a registry.Index
// equivalent to what the portal would have emitted as index.json.
//
// Version/hash fields are populated with wire-protocol sentinels
// ("latest" / empty) — the v2 catalog does not carry per-component
// version metadata, and the version flow is owned by the per-component
// frontmatter (read via Manifest()) or the wire-protocol HTTPRegistry.
// Callers needing precise version/hash pairs MUST use one of those
// transports; this YAML fallback exists so the *catalog listing*
// (search, doctor.Index().Reachable, fdh install resolution) works
// against a direct git clone of the hub.
func readHubCatalog(path string) (Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Index{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cat hubCatalog
	if err := yaml.Unmarshal(data, &cat); err != nil {
		return Index{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if cat.SchemaVersion != 2 {
		return Index{}, fmt.Errorf(
			"%s: schema_version=%d (this fdh's YAML fallback supports schema_version=2 only)",
			path, cat.SchemaVersion,
		)
	}
	out := Index{
		SchemaVersion: 2,
		Registry:      "git+yaml",
		Components:    make([]IndexEntry, 0, len(cat.Components)),
	}
	for _, c := range cat.Components {
		out.Components = append(out.Components, IndexEntry{
			Kind:          c.Kind,
			Namespace:     deriveNamespaceFromOwnerTeam(c.OwnerTeam),
			Name:          c.Name,
			Description:   c.Description,
			OwnerTeam:     c.OwnerTeam,
			Tags:          c.Tags,
			LatestVersion: "latest",
			LatestHash:    "",
			ScanStatus:    "none",
		})
	}
	out.normalize()
	return out, nil
}

// deriveNamespaceFromOwnerTeam mirrors portalapi.deriveNamespace: the
// canonical hub-http-registry namespace rule. Lowercase, "_"→"-", drop
// non-[a-z0-9-] chars, trim leading/trailing "-", fall back to
// "unknown" when the input collapses to empty.
//
// Duplicated (not imported) so pkg/registry stays import-cycle-free from
// internal/portalapi. If the canonical rule ever changes, grep for both
// identifiers and update them together.
func deriveNamespaceFromOwnerTeam(ownerTeam string) string {
	s := strings.ToLower(strings.TrimSpace(ownerTeam))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}

func readManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read %s: %w", path, err)
	}
	var m Manifest
	if err := unmarshalStrict(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}

func locateBundleDir(extractedRoot string) (string, error) {
	entries, err := os.ReadDir(extractedRoot)
	if err != nil {
		return "", err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) != 1 {
		return "", fmt.Errorf("expected exactly one top-level directory in bundle.tar.gz, got %d", len(dirs))
	}
	return filepath.Join(extractedRoot, dirs[0]), nil
}

func extractTarGz(tgz, dest string) error {
	f, err := os.Open(tgz)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dest, filepath.FromSlash(hdr.Name))
		// Guard against path traversal.
		if !strings.HasPrefix(filepath.Clean(target)+string(filepath.Separator), filepath.Clean(dest)+string(filepath.Separator)) {
			return fmt.Errorf("tar entry escapes destination: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA: //nolint:staticcheck // TypeRegA kept for backward compat
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return err
			}
			_ = out.Close()
			// Preserve executable bit on POSIX.
			if hdr.Mode&0o111 != 0 {
				_ = os.Chmod(target, 0o755)
			}
		default:
			// Other types (symlinks, hardlinks, special files) are not
			// expected in bundles. Ignore to be safe.
		}
	}
	return nil
}

// systemGitAvailable reports whether the system `git` binary is on PATH.
// Used to decide whether the auth-error fallback path is even possible.
func systemGitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "authentication") ||
		strings.Contains(msg, "authorization") ||
		strings.Contains(msg, "permission denied")
}

func systemGitClone(ctx context.Context, url, dest, branch string) error {
	args := []string{"clone", "--single-branch", "--branch", branch, url, dest}
	cmd := exec.CommandContext(ctx, "git", args...)
	return cmd.Run()
}

func systemGitFetch(ctx context.Context, clonePath string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", clonePath, "fetch", "--all", "--prune")
	return cmd.Run()
}

// systemGitHardReset is the shell-out equivalent of go-git's Worktree.Checkout
// with Force=true. Used by the refresh fallback when go-git's worktree
// manipulation fails in corporate environments.
func systemGitHardReset(ctx context.Context, clonePath, ref string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", clonePath, "reset", "--hard", ref)
	return cmd.Run()
}
