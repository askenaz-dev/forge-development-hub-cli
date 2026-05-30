package portalapi_test

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/pkg/registry"
)

// TestWireManifest_GitTagVersions proves the per-component SemVer producer:
// versions[] is built from `<kind-plural>/<name>@<semver>` git tags on the hub
// clone, each with its own historical content_hash and the tag commit's
// committer time as published_at; the tip (frontmatter version) is latest.
func TestWireManifest_GitTagVersions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	hub := t.TempDir()

	writeFixtureFile(t, filepath.Join(hub, "hub", "registry.yaml"),
		"schema_version: 2\n"+
			"hub_version: \"test\"\n"+
			"components:\n"+
			"  - name: demo\n"+
			"    kind: skill\n"+
			"    description: demo component\n"+
			"    owner_team: dx-platform\n"+
			"    tags: []\n"+
			"    default: false\n"+
			"    min_fdh_version: \"0.4.0\"\n"+
			"    agents_supported: [claude-code]\n"+
			"    path: skills/demo\n")
	skill := filepath.Join(hub, "skills", "demo", "SKILL.md")

	runGit(t, hub, nil, "init", "-q")

	// v0.1.0
	writeFixtureFile(t, skill, "---\nname: demo\nversion: 0.1.0\ndescription: v one\n---\nbody one\n")
	runGit(t, hub, nil, "add", "-A")
	runGit(t, hub, []string{
		"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
	}, "commit", "-qm", "feat(demo): add skill")
	runGit(t, hub, nil, "tag", "skills/demo@0.1.0")

	// v0.2.0 (tip): different content
	writeFixtureFile(t, skill, "---\nname: demo\nversion: 0.2.0\ndescription: v two\n---\nbody two changed\n")
	runGit(t, hub, nil, "add", "-A")
	runGit(t, hub, []string{
		"GIT_AUTHOR_DATE=2026-02-02T00:00:00Z", "GIT_COMMITTER_DATE=2026-02-02T00:00:00Z",
	}, "commit", "-qm", "feat(demo): expand skill")
	runGit(t, hub, nil, "tag", "skills/demo@0.2.0")

	// A stored cosign bundle for 0.2.0 must be surfaced in the manifest's
	// reserved signature field (capability bundle-signing).
	writeFixtureFile(t, filepath.Join(hub, ".sigs", "skills", "demo", "0.2.0.bundle"), "cosign-bundle-xyz\n")

	h := newWireTestServer(t, hub)
	w := do(t, h, http.MethodGet, "/v1/skills/dx-platform/demo/manifest.json", nil)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var m registry.Manifest
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))

	require.Equal(t, "0.2.0", m.Latest)
	require.Len(t, m.Versions, 2)
	require.Equal(t, "0.2.0", m.Versions[0].Version, "newest first")
	require.Equal(t, "0.1.0", m.Versions[1].Version)
	require.NotEqual(t, m.Versions[0].ContentHash, m.Versions[1].ContentHash,
		"each version is hashed over its own historical tree")
	require.Len(t, m.Versions[0].ContentHash, 64)
	require.Contains(t, m.Versions[0].PublishedAt, "2026-02-02", "published_at = tag committer time")
	require.Contains(t, m.Versions[1].PublishedAt, "2026-01-01")
	require.Equal(t, "cosign-bundle-xyz", m.Versions[0].Signature, "stored signature surfaced in manifest")
	require.Empty(t, m.Versions[1].Signature, "no signature stored for 0.1.0")

	// The 0.1.0 bundle must serve its historical content (410/old tree), with a
	// sidecar hash matching the manifest entry.
	side := do(t, h, http.MethodGet, "/v1/skills/dx-platform/demo/versions/0.1.0/bundle.sha256", nil)
	require.Equal(t, http.StatusOK, side.Code)
	require.Contains(t, side.Body.String(), m.Versions[1].ContentHash)
}

func runGit(t *testing.T, dir string, extraEnv []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
