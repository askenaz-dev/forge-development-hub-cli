package portalapi

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/forge/fdh/pkg/bundle"
)

// Per-component SemVer producer (capability component-versioning-and-release).
//
// Versions are derived from per-component git tags `<kind-plural>/<name>@<semver>`
// on the hub clone at FDH_PORTAL_HUB_PATH, with each version's content_hash
// computed over that tag's tree and published_at taken from the tag commit's
// committer time. The frontmatter `version` (written back by release-please) is
// the declared latest; a component with no tag yet publishes that version as-is
// from the working tree. If FDH_PORTAL_HUB_PATH is not a git repo (or carries no
// tags), the producer degrades to a single frontmatter-version entry.

func kindToPlural(kind string) string {
	switch kind {
	case "skill":
		return "skills"
	case "rule":
		return "rules"
	case "agent":
		return "agents"
	case "hook":
		return "hooks"
	default:
		return kind + "s"
	}
}

func entrypointFile(kind string) string {
	switch kind {
	case "skill":
		return "SKILL.md"
	case "rule":
		return "RULE.md"
	case "agent":
		return "AGENT.md"
	case "hook":
		return "HOOK.md"
	default:
		return ""
	}
}

// frontmatterVersion reads the `version` field from a component entrypoint's
// frontmatter. Returns "" if absent or unparseable.
func frontmatterVersion(srcDir, kind string) string {
	// Prefer the kind-specific entrypoint (RULE.md/AGENT.md/HOOK.md), falling
	// back to SKILL.md (which bundles for every kind use by convention when the
	// canonical document is named generically).
	for _, ep := range []string{entrypointFile(kind), "SKILL.md"} {
		if ep == "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(srcDir, ep))
		if err != nil {
			continue
		}
		doc, err := bundle.ParseSkillMD(raw)
		if err != nil || !doc.HasFrontmatter {
			continue
		}
		if v, ok := doc.Raw["version"].(string); ok {
			return strings.TrimSpace(v)
		}
		return "" // entrypoint parsed but declares no version
	}
	return ""
}

var semverRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

// semverKey extracts [major, minor, patch] for ordering. Non-semver strings
// sort lowest.
func semverKey(v string) [3]int {
	m := semverRe.FindStringSubmatch(v)
	if m == nil {
		return [3]int{-1, -1, -1}
	}
	var out [3]int
	for i := 0; i < 3; i++ {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out
}

// semverLess reports whether a < b by (major, minor, patch), with a stable
// string tiebreak (so a release sorts after its own pre-releases deterministically).
func semverLess(a, b string) bool {
	ka, kb := semverKey(a), semverKey(b)
	for i := 0; i < 3; i++ {
		if ka[i] != kb[i] {
			return ka[i] < kb[i]
		}
	}
	return a < b
}

// resolvedVersion is one published version of a component.
//
// Status implements capability `component-lifecycle`: empty/active is
// the default; "deprecated" is still served but flagged; "yanked"
// causes the wire-protocol bundle handler to return 410 Gone.
type resolvedVersion struct {
	Version     string
	ContentHash string
	PublishedAt time.Time
	Signature   string
	Status      string
}

// readSignature returns the stored cosign bundle for a version, if the signing
// pipeline committed one under <hubPath>/.sigs/<plural>/<name>/<version>.bundle.
// Returns "" when no signature is stored (unsigned mirror / pre-signing).
func readSignature(hubPath, plural, name, version string) string {
	p := filepath.Join(hubPath, ".sigs", plural, name, version+".bundle")
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// taggedCommit pairs a tag's version string with the commit it points at.
type taggedCommit struct {
	When   time.Time
	Commit plumbing.Hash
}

// gitTagVersions opens the repo at hubPath and returns the `<plural>/<name>@<ver>`
// tags it carries. The returned repo handle is reused for historical tree reads.
// Returns (nil, nil) if hubPath is not a git repo or carries no matching tags.
func gitTagVersions(hubPath, plural, name string) (map[string]taggedCommit, *gogit.Repository) {
	repo, err := gogit.PlainOpen(hubPath)
	if err != nil {
		return nil, nil // not a git repo → caller falls back
	}
	iter, err := repo.Tags()
	if err != nil {
		return nil, nil
	}
	prefix := plural + "/" + name + "@"
	out := map[string]taggedCommit{}
	_ = iter.ForEach(func(ref *plumbing.Reference) error {
		short := ref.Name().Short()
		if !strings.HasPrefix(short, prefix) {
			return nil
		}
		ver := strings.TrimPrefix(short, prefix)
		commit, err := resolveCommit(repo, ref.Hash())
		if err != nil {
			return nil //nolint:nilerr // skip unresolvable tag
		}
		out[ver] = taggedCommit{When: commit.Committer.When.UTC(), Commit: commit.Hash}
		return nil
	})
	if len(out) == 0 {
		return nil, repo
	}
	return out, repo
}

// resolveCommit dereferences a hash that may be an annotated tag or a commit.
func resolveCommit(repo *gogit.Repository, h plumbing.Hash) (*object.Commit, error) {
	if tag, err := repo.TagObject(h); err == nil {
		return tag.Commit()
	}
	return repo.CommitObject(h)
}

// extractTreeAt materializes the component subtree at compPath (slash form,
// e.g. "skills/design-system") from the given commit into destDir, preserving
// the executable bit. Used both for historical hashing and historical tarball
// builds so they share one extraction.
func extractTreeAt(repo *gogit.Repository, commitHash plumbing.Hash, compPath, destDir string) error {
	commit, err := repo.CommitObject(commitHash)
	if err != nil {
		return err
	}
	tree, err := commit.Tree()
	if err != nil {
		return err
	}
	sub, err := tree.Tree(compPath)
	if err != nil {
		return err
	}
	return sub.Files().ForEach(func(f *object.File) error {
		contents, err := f.Contents()
		if err != nil {
			return err
		}
		dst := filepath.Join(destDir, filepath.FromSlash(f.Name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		perm := os.FileMode(0o644)
		if f.Mode == filemode.Executable {
			perm = 0o755
		}
		return os.WriteFile(dst, []byte(contents), perm)
	})
}

// hashAtCommit extracts the component tree at a commit to a temp dir and
// returns its canonical content hash.
func hashAtCommit(repo *gogit.Repository, commitHash plumbing.Hash, compPath string) (string, error) {
	tmp, err := os.MkdirTemp("", "forge-ver-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := extractTreeAt(repo, commitHash, compPath, tmp); err != nil {
		return "", err
	}
	return bundle.HashDir(tmp)
}

// componentVersions returns the published versions of a component, newest first.
// compPath is the slash-form repo path (comp.Path). srcDir is the working-tree
// path for the tip. The tip's content_hash uses the working tree; older tagged
// versions are hashed from history.
func componentVersions(hubPath, srcDir, compPath, kind, name string) ([]resolvedVersion, error) {
	fmv := frontmatterVersion(srcDir, kind)
	plural := kindToPlural(kind)
	tags, repo := gitTagVersions(hubPath, plural, name)

	tipHash, err := bundle.HashDir(srcDir)
	if err != nil {
		return nil, err
	}

	byVer := map[string]resolvedVersion{}
	for ver, tc := range tags {
		hash := tipHash
		if ver != fmv && repo != nil {
			if h, err := hashAtCommit(repo, tc.Commit, compPath); err == nil {
				hash = h
			} else {
				continue // skip a version whose historical tree can't be read
			}
		}
		byVer[ver] = resolvedVersion{Version: ver, ContentHash: hash, PublishedAt: tc.When}
	}

	// The declared frontmatter version is always present, even before its tag
	// exists (the transient post-merge / pre-release-tag window).
	if fmv != "" {
		if _, ok := byVer[fmv]; !ok {
			byVer[fmv] = resolvedVersion{Version: fmv, ContentHash: tipHash, PublishedAt: wirePublishedAtTime()}
		}
	}
	if len(byVer) == 0 {
		// No frontmatter version and no tags: serve a single sentinel so the
		// manifest stays well-formed (versions[] must be non-empty).
		byVer["0.0.0"] = resolvedVersion{Version: "0.0.0", ContentHash: tipHash, PublishedAt: wirePublishedAtTime()}
	}

	out := make([]resolvedVersion, 0, len(byVer))
	for _, v := range byVer {
		out = append(out, v)
	}
	for i := range out {
		out[i].Signature = readSignature(hubPath, plural, name, out[i].Version)
	}
	sort.Slice(out, func(i, j int) bool { return semverLess(out[j].Version, out[i].Version) })
	return out, nil
}

// resolveVersionSource resolves a specific requested version to a source
// directory for tarball/sidecar building. The tip version (== frontmatter
// version) is served from the working tree; an older tagged version is
// extracted from history into a temp dir. The returned cleanup MUST be called
// by the handler (it is a no-op for the working-tree case). found is false when
// the version is not published for this component (→ 404).
func resolveVersionSource(hubPath, srcDir, compPath, kind, name, version string) (
	dir string, cleanup func(), rv resolvedVersion, found bool, err error,
) {
	cleanup = func() {}

	vers, err := componentVersions(hubPath, srcDir, compPath, kind, name)
	if err != nil {
		return "", cleanup, rv, false, err
	}
	for i := range vers {
		if vers[i].Version == version {
			rv = vers[i]
			found = true
			break
		}
	}
	if !found {
		return "", cleanup, rv, false, nil
	}

	// The tip (declared frontmatter version) is served from the working tree.
	if version == frontmatterVersion(srcDir, kind) {
		return srcDir, cleanup, rv, true, nil
	}

	// Older version → extract its tagged tree from history.
	plural := kindToPlural(kind)
	tags, repo := gitTagVersions(hubPath, plural, name)
	tc, ok := tags[version]
	if !ok || repo == nil {
		// In the catalog versions[] but no resolvable tag: fall back to the
		// working tree rather than 404 (best-effort).
		return srcDir, cleanup, rv, true, nil
	}
	tmp, err := os.MkdirTemp("", "forge-bundle-*")
	if err != nil {
		return "", cleanup, rv, false, err
	}
	if err := extractTreeAt(repo, tc.Commit, compPath, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", cleanup, rv, false, err
	}
	return tmp, func() { _ = os.RemoveAll(tmp) }, rv, true, nil
}
