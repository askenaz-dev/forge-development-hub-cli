package hubregistry_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/forge/fdh/pkg/hubregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// golden fixture used across the tests: a minimal valid registry.yaml
// referencing two skills, both of which exist on disk.
const fixtureRegistry = `schema_version: 1
generated_at: 2026-05-23T00:00:00Z
skills:
  - name: design-system
    path: skills/design-system
    default: true
    agents_supported: [claude-code, codex]
    description: "Shared design tokens and components"
    version: "2026.05"
    min_fdh_version: "0.5.0"
  - name: code-review
    path: skills/code-review
    default: false
    agents_supported: [claude-code, copilot]
    description: "Senior-grade PR review playbook"
`

// buildHubFixture lays out a git repo with the fixture registry
// committed, so Load can read HEAD without any network.
func buildHubFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("system git not available; hubregistry tests need it")
	}
	dir := t.TempDir()
	// init + first commit with the registry + the two skill dirs.
	mustGit(t, dir, "init", "--initial-branch", "main")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "Test")

	skillsDir := filepath.Join(dir, "skills")
	require.NoError(t, os.MkdirAll(filepath.Join(skillsDir, "design-system"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(skillsDir, "code-review"), 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(skillsDir, "registry.yaml"), []byte(fixtureRegistry), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(skillsDir, "design-system", "SKILL.md"), []byte("---\nname: design-system\n---\n# Design system\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(skillsDir, "code-review", "SKILL.md"), []byte("---\nname: code-review\n---\n# Code review\n"), 0o644))

	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-m", "fixture")
	return dir
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, string(out))
}

func TestLoad_FromLocalFixture(t *testing.T) {
	hub := buildHubFixture(t)
	cache := t.TempDir()

	reg, err := hubregistry.Load(context.Background(), hub, hubregistry.LoadOptions{
		CacheDir: cache,
		Branch:   "main",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, reg.SchemaVersion)
	assert.Len(t, reg.Skills, 2)
	assert.Equal(t, "design-system", reg.Skills[0].Name)
	assert.True(t, reg.Skills[0].Default)
	assert.NotEmpty(t, reg.HubCommit)
	assert.Equal(t, cache, reg.LocalPath)
}

func TestLoad_RecoverFromCorruptCache(t *testing.T) {
	cache := t.TempDir()
	// Put a non-git file at the cache root so Load reports corruption.
	require.NoError(t, os.WriteFile(filepath.Join(cache, "stray"), []byte("x"), 0o644))

	_, err := hubregistry.Load(context.Background(), "file:///nowhere", hubregistry.LoadOptions{
		CacheDir: cache,
	})
	require.Error(t, err)
	var corrupt *hubregistry.CorruptCacheError
	require.ErrorAs(t, err, &corrupt, "expected CorruptCacheError, got %T: %v", err, err)

	// Recover, then re-running Load against a valid hub succeeds.
	require.NoError(t, hubregistry.RecoverFromCorruption(cache))
	hub := buildHubFixture(t)
	reg, err := hubregistry.Load(context.Background(), hub, hubregistry.LoadOptions{CacheDir: cache})
	require.NoError(t, err)
	assert.Len(t, reg.Skills, 2)
}

func TestLoad_FreshClonePopulatesRegistryOnly(t *testing.T) {
	hub := buildHubFixture(t)
	cache := t.TempDir()

	reg, err := hubregistry.Load(context.Background(), hub, hubregistry.LoadOptions{CacheDir: cache})
	require.NoError(t, err)
	_ = reg

	// The sparse-checkout pattern is `skills/registry.yaml` only, so
	// the per-skill directories should NOT be materialised yet.
	_, err = os.Stat(filepath.Join(cache, "skills", "registry.yaml"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(cache, "skills", "design-system", "SKILL.md"))
	assert.Error(t, err, "skill dir should not be materialised before FetchSkill")
}

func TestFetchSkill_ExtendsSparseCheckout(t *testing.T) {
	hub := buildHubFixture(t)
	cache := t.TempDir()
	reg, err := hubregistry.Load(context.Background(), hub, hubregistry.LoadOptions{CacheDir: cache})
	require.NoError(t, err)

	path, err := reg.FetchSkill(context.Background(), "design-system")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cache, "skills", "design-system"), path)

	_, err = os.Stat(filepath.Join(path, "SKILL.md"))
	assert.NoError(t, err)
}

func TestFetchSkill_UnknownReturnsError(t *testing.T) {
	hub := buildHubFixture(t)
	cache := t.TempDir()
	reg, err := hubregistry.Load(context.Background(), hub, hubregistry.LoadOptions{CacheDir: cache})
	require.NoError(t, err)

	_, err = reg.FetchSkill(context.Background(), "nonexistent")
	assert.Error(t, err)
}

func TestValidate_GoldenFixturePasses(t *testing.T) {
	hub := buildHubFixture(t)
	cache := t.TempDir()
	reg, err := hubregistry.Load(context.Background(), hub, hubregistry.LoadOptions{CacheDir: cache})
	require.NoError(t, err)

	// Pull each skill so path-exists check has the dirs on disk.
	_, err = reg.FetchSkill(context.Background(), "design-system")
	require.NoError(t, err)
	_, err = reg.FetchSkill(context.Background(), "code-review")
	require.NoError(t, err)

	res := hubregistry.Validate(reg, cache)
	assert.True(t, res.OK, "errors: %+v", res.Errors)
}

func TestValidate_DuplicateName(t *testing.T) {
	reg := &hubregistry.Registry{
		SchemaVersion: 1,
		Skills: []hubregistry.SkillEntry{
			{Name: "dup", Path: "skills/dup", AgentsSupported: []string{"claude-code"}},
			{Name: "dup", Path: "skills/other", AgentsSupported: []string{"codex"}},
		},
	}
	res := hubregistry.Validate(reg, "")
	require.False(t, res.OK)
	assertRule(t, res.Errors, "unique-name")
}

func TestValidate_NonKebabName(t *testing.T) {
	reg := &hubregistry.Registry{
		SchemaVersion: 1,
		Skills: []hubregistry.SkillEntry{
			{Name: "Bad_Name", Path: "skills/x", AgentsSupported: []string{"claude-code"}},
		},
	}
	res := hubregistry.Validate(reg, "")
	require.False(t, res.OK)
	assertRule(t, res.Errors, "name-kebab-case")
}

func TestValidate_EmptyAgentsSupported(t *testing.T) {
	reg := &hubregistry.Registry{
		SchemaVersion: 1,
		Skills: []hubregistry.SkillEntry{
			{Name: "x", Path: "skills/x", AgentsSupported: nil},
		},
	}
	res := hubregistry.Validate(reg, "")
	require.False(t, res.OK)
	assertRule(t, res.Errors, "agents-supported-nonempty")
}

func TestValidate_InvalidSemver(t *testing.T) {
	reg := &hubregistry.Registry{
		SchemaVersion: 1,
		Skills: []hubregistry.SkillEntry{
			{Name: "x", Path: "skills/x", AgentsSupported: []string{"claude-code"}, MinFDHVersion: "not-a-version"},
		},
	}
	res := hubregistry.Validate(reg, "")
	require.False(t, res.OK)
	assertRule(t, res.Errors, "semver")
}

func TestValidate_UnknownSchemaVersion(t *testing.T) {
	reg := &hubregistry.Registry{
		SchemaVersion: 99,
		Skills:        nil,
	}
	res := hubregistry.Validate(reg, "")
	require.False(t, res.OK)
	assertRule(t, res.Errors, "schema-version")
}

func TestValidate_PathDoesNotExist(t *testing.T) {
	hub := buildHubFixture(t)
	cache := t.TempDir()
	reg, err := hubregistry.Load(context.Background(), hub, hubregistry.LoadOptions{CacheDir: cache})
	require.NoError(t, err)
	reg.Skills = append(reg.Skills, hubregistry.SkillEntry{
		Name:            "ghost",
		Path:            "skills/ghost",
		AgentsSupported: []string{"claude-code"},
	})
	res := hubregistry.Validate(reg, cache)
	require.False(t, res.OK)
	assertRule(t, res.Errors, "path-exists")
}

func TestValidate_Orphan(t *testing.T) {
	hub := buildHubFixture(t)
	cache := t.TempDir()
	reg, err := hubregistry.Load(context.Background(), hub, hubregistry.LoadOptions{CacheDir: cache})
	require.NoError(t, err)
	// Materialise both skill dirs first so the orphan detector has
	// directories to walk.
	_, err = reg.FetchSkill(context.Background(), "design-system")
	require.NoError(t, err)
	_, err = reg.FetchSkill(context.Background(), "code-review")
	require.NoError(t, err)
	// Drop one entry so the existing dir becomes an orphan.
	reg.Skills = reg.Skills[:1]
	res := hubregistry.Validate(reg, cache)
	require.False(t, res.OK)
	assertRule(t, res.Errors, "no-orphans")
}

func TestDefaultCacheDir_NonEmpty(t *testing.T) {
	got := hubregistry.DefaultCacheDir()
	assert.NotEmpty(t, got)
}

func assertRule(t *testing.T, errs []hubregistry.ValidationError, rule string) {
	t.Helper()
	for _, e := range errs {
		if e.Rule == rule {
			return
		}
	}
	t.Fatalf("expected an error with rule %q, got %+v", rule, errs)
}
