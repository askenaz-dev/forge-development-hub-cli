package registry_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/internal/testutil"
	"github.com/forge/fdh/pkg/registry"
)

func buildFixtureRegistry(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	testutil.BuildRegistry(t, root, []testutil.SkillSpec{
		{
			Namespace:   "code-review",
			Name:        "standard",
			Version:     "1.0.0",
			Description: "Standard code review checklist.",
			OwnerTeam:   "dx",
			Tags:        []string{"review", "quality"},
			Files: map[string]string{
				"SKILL.md": testutil.FixtureSKILLMD("standard", "Standard code review checklist."),
			},
		},
		{
			Namespace:   "security",
			Name:        "owasp-review",
			Version:     "1.2.0",
			Description: "Run an OWASP top-10 sweep.",
			OwnerTeam:   "appsec",
			Tags:        []string{"owasp", "security"},
			Files: map[string]string{
				"SKILL.md":            testutil.FixtureSKILLMD("owasp-review", "Run an OWASP top-10 sweep."),
				"references/owasp.md": "Top 10 ...",
			},
		},
	})
	return root
}

func newGitRegistry(t *testing.T, root string) *registry.GitRegistry {
	t.Helper()
	return &registry.GitRegistry{
		LocalPath: root,
		SkipFetch: true, // tests use a hand-built directory, not a real clone
	}
}

func TestGitRegistry_Source(t *testing.T) {
	r := newGitRegistry(t, t.TempDir())
	assert.Contains(t, r.Source(), "git:")
}

func TestGitRegistry_Index(t *testing.T) {
	root := buildFixtureRegistry(t)
	r := newGitRegistry(t, root)
	idx, err := r.Index(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, idx.SchemaVersion)
	assert.Len(t, idx.Skills, 2)

	var ns []string
	for _, s := range idx.Skills {
		ns = append(ns, s.Namespace+"/"+s.Name)
	}
	assert.ElementsMatch(t, []string{"code-review/standard", "security/owasp-review"}, ns)
}

func TestGitRegistry_Manifest(t *testing.T) {
	root := buildFixtureRegistry(t)
	r := newGitRegistry(t, root)
	m, err := r.Manifest(context.Background(), "security", "owasp-review")
	require.NoError(t, err)
	assert.Equal(t, "owasp-review", m.Name)
	assert.Equal(t, "1.2.0", m.Latest)
	v := m.FindVersion("1.2.0")
	require.NotNil(t, v)
	assert.Equal(t, "pass", v.ScanStatus)
}

func TestGitRegistry_FetchBundle(t *testing.T) {
	root := buildFixtureRegistry(t)
	r := newGitRegistry(t, root)

	bp, err := r.FetchBundle(context.Background(), "security", "owasp-review", "1.2.0")
	require.NoError(t, err)
	defer bp.Cleanup()

	// Extracted bundle contains SKILL.md.
	skillPath := filepath.Join(bp.Path, "SKILL.md")
	_, err = os.Stat(skillPath)
	require.NoError(t, err)

	// And the hash is non-empty.
	assert.NotEmpty(t, bp.Hash)
}

func TestGitRegistry_FetchBundle_HashMismatchDetected(t *testing.T) {
	root := buildFixtureRegistry(t)
	r := newGitRegistry(t, root)

	// Corrupt the recorded sha256 to force a mismatch.
	sumFile := filepath.Join(root, "skills", "code-review", "standard", "versions", "1.0.0", "bundle.sha256")
	require.NoError(t, os.WriteFile(sumFile, []byte("0000000000000000000000000000000000000000000000000000000000000000  bundle.tar.gz\n"), 0o644))

	_, err := r.FetchBundle(context.Background(), "code-review", "standard", "1.0.0")
	require.Error(t, err)
	var hm registry.HashMismatch
	assert.ErrorAs(t, err, &hm)
}

func TestGitRegistry_Search(t *testing.T) {
	root := buildFixtureRegistry(t)
	r := newGitRegistry(t, root)

	results, err := r.Search(context.Background(), "owasp")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "owasp-review", results[0].Name)

	empty, err := r.Search(context.Background(), "nonexistent-term")
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestGitRegistry_CheckConsistency_Clean(t *testing.T) {
	root := buildFixtureRegistry(t)
	r := newGitRegistry(t, root)
	issues := r.CheckConsistency(context.Background())
	assert.Empty(t, issues)
}

func TestGitRegistry_CheckConsistency_DriftDetected(t *testing.T) {
	root := buildFixtureRegistry(t)

	// Edit the INDEX to claim a different latest_hash than the manifest.
	// That's the kind of drift CheckConsistency is meant to catch.
	idxPath := filepath.Join(root, "index.json")
	idxBytes, err := os.ReadFile(idxPath)
	require.NoError(t, err)
	// Replace any hex hash in the index with all zeros.
	corrupted := []byte(`{"schema_version":1,"registry":"x","skills":[` +
		`{"namespace":"code-review","name":"standard","description":"x","owner_team":"dx",` +
		`"latest_version":"1.0.0",` +
		`"latest_hash":"0000000000000000000000000000000000000000000000000000000000000000",` +
		`"scan_status":"pass"}]}`)
	_ = idxBytes
	require.NoError(t, os.WriteFile(idxPath, corrupted, 0o644))

	r := newGitRegistry(t, root)
	issues := r.CheckConsistency(context.Background())
	require.NotEmpty(t, issues, "expected at least one consistency issue")
	found := false
	for _, iss := range issues {
		if iss.Severity == "warning" && iss.Skill == "code-review/standard" {
			found = true
		}
	}
	assert.True(t, found, "expected a warning for code-review/standard, got: %+v", issues)
}

func TestGitRegistry_MissingLocalPath(t *testing.T) {
	r := &registry.GitRegistry{LocalPath: filepath.Join(t.TempDir(), "nope")}
	_, err := r.Index(context.Background())
	require.Error(t, err)
	var unreach registry.RegistryUnreachable
	assert.ErrorAs(t, err, &unreach)
}

func TestGitRegistry_StrictJSON_RejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "index.json"),
		[]byte(`{"schema_version":1,"registry":"x","skills":[],"bogus":"hi"}`), 0o644))
	r := newGitRegistry(t, root)
	_, err := r.Index(context.Background())
	require.Error(t, err)
}
