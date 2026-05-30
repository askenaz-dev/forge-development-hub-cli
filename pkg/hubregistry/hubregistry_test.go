package hubregistry_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/pkg/hubregistry"
)

// v2 fixture: the schema-of-record post hub-registry-v2.
const fixtureRegistryV2 = `schema_version: 2
hub_version: "2026.05-test"
components:
  - name: design-system
    kind: skill
    path: skills/design-system
    owner_team: dx-platform
    default: true
    agents_supported: [claude-code, codex]
    description: "Shared design tokens and components"
    version: "0.4.0"
    min_fdh_version: "0.5.0"
  - name: code-review
    kind: skill
    path: skills/code-review
    owner_team: dx-platform
    default: false
    agents_supported: [claude-code, copilot]
    description: "Senior-grade PR review playbook"
`

// v1 fixture: the legacy mirror shape. Used to test the normalizer +
// deprecation warning.
const fixtureRegistryV1 = `schema_version: 1
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

// buildHubFixture writes a v2 catalog at hub/registry.yaml plus the
// two skill source directories, committed to a fresh git repo so
// Load can read HEAD without any network.
func buildHubFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("system git not available; hubregistry tests need it")
	}
	dir := t.TempDir()
	mustGit(t, dir, "init", "--initial-branch", "main")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "Test")

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "hub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hub", "registry.yaml"), []byte(fixtureRegistryV2), 0o644))

	skillsDir := filepath.Join(dir, "skills")
	require.NoError(t, os.MkdirAll(filepath.Join(skillsDir, "design-system"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(skillsDir, "code-review"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillsDir, "design-system", "SKILL.md"), []byte("---\nname: design-system\n---\n# Design system\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(skillsDir, "code-review", "SKILL.md"), []byte("---\nname: code-review\n---\n# Code review\n"), 0o644))

	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-m", "fixture")
	return dir
}

// buildHubFixtureV1 builds the same content but only writes the
// legacy v1 mirror at skills/registry.yaml (no hub/registry.yaml).
// Used to drive the v1 normalizer + deprecation warning path.
func buildHubFixtureV1(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("system git not available; hubregistry tests need it")
	}
	dir := t.TempDir()
	mustGit(t, dir, "init", "--initial-branch", "main")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "Test")

	skillsDir := filepath.Join(dir, "skills")
	require.NoError(t, os.MkdirAll(filepath.Join(skillsDir, "design-system"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(skillsDir, "code-review"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillsDir, "registry.yaml"), []byte(fixtureRegistryV1), 0o644))
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

func TestLoad_V2_FromLocalFixture(t *testing.T) {
	hub := buildHubFixture(t)
	cache := t.TempDir()

	reg, err := hubregistry.Load(context.Background(), hub, hubregistry.LoadOptions{
		CacheDir: cache,
		Branch:   "main",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, reg.SchemaVersion)
	assert.Equal(t, "2026.05-test", reg.HubVersion)
	assert.Len(t, reg.Components, 2)
	assert.Equal(t, "design-system", reg.Components[0].Name)
	assert.Equal(t, hubregistry.KindSkill, reg.Components[0].Kind)
	assert.True(t, reg.Components[0].Default)
	// Derived Skills view stays populated for back-compat.
	assert.Len(t, reg.Skills, 2)
	assert.Equal(t, "design-system", reg.Skills[0].Name)
	assert.True(t, reg.Skills[0].Default)
	assert.NotEmpty(t, reg.HubCommit)
	assert.Equal(t, cache, reg.LocalPath)
}

func TestLoad_V1_NormalizesAndWarns(t *testing.T) {
	hub := buildHubFixtureV1(t)
	cache := t.TempDir()

	var logged []string
	reg, err := hubregistry.Load(context.Background(), hub, hubregistry.LoadOptions{
		CacheDir: cache,
		Branch:   "main",
		Logger:   func(s string) { logged = append(logged, s) },
	})
	require.NoError(t, err)
	assert.Equal(t, 1, reg.SchemaVersion)
	// Normalizer should have produced Components with Kind=skill.
	assert.Len(t, reg.Components, 2)
	assert.Equal(t, hubregistry.KindSkill, reg.Components[0].Kind)
	// Skills view also populated.
	assert.Len(t, reg.Skills, 2)
	// Logger should have received a deprecation warning.
	found := false
	for _, line := range logged {
		if strings.Contains(line, "deprecated") && strings.Contains(line, "2026-07-22") {
			found = true
			break
		}
	}
	assert.Truef(t, found, "expected a deprecation warning naming 2026-07-22 in log: %v", logged)
}

func TestLoad_UnknownSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, "init", "--initial-branch", "main")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "Test")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "hub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hub", "registry.yaml"),
		[]byte("schema_version: 7\ncomponents: []\n"), 0o644))
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-m", "bad")

	cache := t.TempDir()
	_, err := hubregistry.Load(context.Background(), dir, hubregistry.LoadOptions{CacheDir: cache})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema_version 7")
}

func TestLoad_RecoverFromCorruptCache(t *testing.T) {
	cache := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cache, "stray"), []byte("x"), 0o644))

	_, err := hubregistry.Load(context.Background(), "file:///nowhere", hubregistry.LoadOptions{
		CacheDir: cache,
	})
	require.Error(t, err)
	var corrupt *hubregistry.CorruptCacheError
	require.ErrorAs(t, err, &corrupt, "expected CorruptCacheError, got %T: %v", err, err)

	require.NoError(t, hubregistry.RecoverFromCorruption(cache))
	hub := buildHubFixture(t)
	reg, err := hubregistry.Load(context.Background(), hub, hubregistry.LoadOptions{CacheDir: cache})
	require.NoError(t, err)
	assert.Len(t, reg.Components, 2)
}

func TestLoad_FreshClonePopulatesCatalogOnly(t *testing.T) {
	hub := buildHubFixture(t)
	cache := t.TempDir()

	reg, err := hubregistry.Load(context.Background(), hub, hubregistry.LoadOptions{CacheDir: cache})
	require.NoError(t, err)
	_ = reg

	// Sparse-checkout limits to the two catalog paths.
	_, err = os.Stat(filepath.Join(cache, "hub", "registry.yaml"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(cache, "skills", "design-system", "SKILL.md"))
	assert.Error(t, err, "skill dir should not be materialized before FetchComponent")
}

func TestFetchComponent_ExtendsSparseCheckout(t *testing.T) {
	hub := buildHubFixture(t)
	cache := t.TempDir()
	reg, err := hubregistry.Load(context.Background(), hub, hubregistry.LoadOptions{CacheDir: cache})
	require.NoError(t, err)

	path, err := reg.FetchComponent(context.Background(), "design-system", hubregistry.KindSkill)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cache, "skills", "design-system"), path)

	_, err = os.Stat(filepath.Join(path, "SKILL.md"))
	assert.NoError(t, err)
}

func TestFetchComponent_UnknownReturnsError(t *testing.T) {
	hub := buildHubFixture(t)
	cache := t.TempDir()
	reg, err := hubregistry.Load(context.Background(), hub, hubregistry.LoadOptions{CacheDir: cache})
	require.NoError(t, err)

	_, err = reg.FetchComponent(context.Background(), "nonexistent", hubregistry.KindRule)
	assert.Error(t, err)
}

func TestFetchSkill_ShimRoutesToFetchComponent(t *testing.T) {
	hub := buildHubFixture(t)
	cache := t.TempDir()
	reg, err := hubregistry.Load(context.Background(), hub, hubregistry.LoadOptions{CacheDir: cache})
	require.NoError(t, err)

	path, err := reg.FetchSkill(context.Background(), "design-system")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cache, "skills", "design-system"), path)
}

func TestComponentByName_LookupHits(t *testing.T) {
	reg := &hubregistry.Registry{
		Components: []hubregistry.ComponentEntry{
			{Name: "x", Kind: hubregistry.KindRule, Path: "rules/x", AgentsSupported: []string{"claude-code"}},
			{Name: "x", Kind: hubregistry.KindSkill, Path: "skills/x", AgentsSupported: []string{"claude-code"}},
		},
	}
	got := reg.ComponentByName("x", hubregistry.KindRule)
	require.NotNil(t, got)
	assert.Equal(t, hubregistry.KindRule, got.Kind)
	assert.Nil(t, reg.ComponentByName("x", hubregistry.KindAgent))
}

func TestComponentsByKind_Filters(t *testing.T) {
	reg := &hubregistry.Registry{
		Components: []hubregistry.ComponentEntry{
			{Name: "a", Kind: hubregistry.KindSkill},
			{Name: "b", Kind: hubregistry.KindSkill},
			{Name: "c", Kind: hubregistry.KindHook},
		},
	}
	skills := reg.ComponentsByKind(hubregistry.KindSkill)
	require.Len(t, skills, 2)
	hooks := reg.ComponentsByKind(hubregistry.KindHook)
	require.Len(t, hooks, 1)
	agents := reg.ComponentsByKind(hubregistry.KindAgent)
	require.NotNil(t, agents)
	assert.Len(t, agents, 0)
}

func TestValidate_GoldenFixturePasses(t *testing.T) {
	hub := buildHubFixture(t)
	cache := t.TempDir()
	reg, err := hubregistry.Load(context.Background(), hub, hubregistry.LoadOptions{CacheDir: cache})
	require.NoError(t, err)

	// Pull each skill so path-exists check has the dirs on disk.
	_, err = reg.FetchComponent(context.Background(), "design-system", hubregistry.KindSkill)
	require.NoError(t, err)
	_, err = reg.FetchComponent(context.Background(), "code-review", hubregistry.KindSkill)
	require.NoError(t, err)

	res := hubregistry.Validate(reg, cache)
	assert.True(t, res.OK, "errors: %+v", res.Errors)
}

func TestValidate_KindRequired(t *testing.T) {
	reg := &hubregistry.Registry{
		SchemaVersion: 2,
		Components: []hubregistry.ComponentEntry{
			{Name: "x", Path: "skills/x", AgentsSupported: []string{"claude-code"}},
		},
	}
	res := hubregistry.Validate(reg, "")
	require.False(t, res.OK)
	assertRule(t, res.Errors, "kind-required")
}

func TestValidate_KindInvalid(t *testing.T) {
	reg := &hubregistry.Registry{
		SchemaVersion: 2,
		Components: []hubregistry.ComponentEntry{
			{Name: "x", Kind: "prompt", Path: "skills/x", AgentsSupported: []string{"claude-code"}},
		},
	}
	res := hubregistry.Validate(reg, "")
	require.False(t, res.OK)
	assertRule(t, res.Errors, "kind-invalid")
}

func TestValidate_KindPathMismatch(t *testing.T) {
	reg := &hubregistry.Registry{
		SchemaVersion: 2,
		Components: []hubregistry.ComponentEntry{
			{Name: "x", Kind: hubregistry.KindRule, Path: "skills/x", AgentsSupported: []string{"claude-code"}},
		},
	}
	res := hubregistry.Validate(reg, "")
	require.False(t, res.OK)
	assertRule(t, res.Errors, "kind-path-mismatch")
}

func TestValidate_DuplicateNamePerKind(t *testing.T) {
	reg := &hubregistry.Registry{
		SchemaVersion: 2,
		Components: []hubregistry.ComponentEntry{
			{Name: "dup", Kind: hubregistry.KindSkill, Path: "skills/dup", AgentsSupported: []string{"claude-code"}},
			{Name: "dup", Kind: hubregistry.KindSkill, Path: "skills/dup", AgentsSupported: []string{"codex"}},
		},
	}
	res := hubregistry.Validate(reg, "")
	require.False(t, res.OK)
	assertRule(t, res.Errors, "unique-name")
}

func TestValidate_SameNameAcrossKindsPasses(t *testing.T) {
	reg := &hubregistry.Registry{
		SchemaVersion: 2,
		Components: []hubregistry.ComponentEntry{
			{Name: "shared", Kind: hubregistry.KindSkill, Path: "skills/shared", AgentsSupported: []string{"claude-code"}},
			{Name: "shared", Kind: hubregistry.KindRule, Path: "rules/shared", AgentsSupported: []string{"claude-code"}},
		},
	}
	res := hubregistry.Validate(reg, "")
	// Should not have a unique-name error.
	for _, e := range res.Errors {
		assert.NotEqual(t, "unique-name", e.Rule, "unexpected unique-name error for shared cross-kind name")
	}
}

func TestValidate_NonKebabName(t *testing.T) {
	reg := &hubregistry.Registry{
		SchemaVersion: 2,
		Components: []hubregistry.ComponentEntry{
			{Name: "Bad_Name", Kind: hubregistry.KindSkill, Path: "skills/Bad_Name", AgentsSupported: []string{"claude-code"}},
		},
	}
	res := hubregistry.Validate(reg, "")
	require.False(t, res.OK)
	assertRule(t, res.Errors, "name-kebab-case")
}

func TestValidate_EmptyAgentsSupported(t *testing.T) {
	reg := &hubregistry.Registry{
		SchemaVersion: 2,
		Components: []hubregistry.ComponentEntry{
			{Name: "x", Kind: hubregistry.KindSkill, Path: "skills/x", AgentsSupported: nil},
		},
	}
	res := hubregistry.Validate(reg, "")
	require.False(t, res.OK)
	assertRule(t, res.Errors, "agents-supported-nonempty")
}

func TestValidate_UnknownAgent(t *testing.T) {
	reg := &hubregistry.Registry{
		SchemaVersion: 2,
		Components: []hubregistry.ComponentEntry{
			{Name: "x", Kind: hubregistry.KindSkill, Path: "skills/x", AgentsSupported: []string{"windsurf"}},
		},
	}
	res := hubregistry.Validate(reg, "")
	require.False(t, res.OK)
	assertRule(t, res.Errors, "agents-supported-unknown")
}

func TestValidate_InvalidSemver(t *testing.T) {
	reg := &hubregistry.Registry{
		SchemaVersion: 2,
		Components: []hubregistry.ComponentEntry{
			{Name: "x", Kind: hubregistry.KindSkill, Path: "skills/x", AgentsSupported: []string{"claude-code"}, MinFDHVersion: "not-a-version"},
		},
	}
	res := hubregistry.Validate(reg, "")
	require.False(t, res.OK)
	assertRule(t, res.Errors, "semver")
}

func TestValidate_UnknownSchemaVersion(t *testing.T) {
	reg := &hubregistry.Registry{
		SchemaVersion: 99,
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
	reg.Components = append(reg.Components, hubregistry.ComponentEntry{
		Name:            "ghost",
		Kind:            hubregistry.KindSkill,
		Path:            "skills/ghost",
		AgentsSupported: []string{"claude-code"},
	})
	res := hubregistry.Validate(reg, cache)
	require.False(t, res.OK)
	assertRule(t, res.Errors, "path-exists")
}

func TestValidate_OrphanPerKind(t *testing.T) {
	hub := buildHubFixture(t)
	cache := t.TempDir()
	reg, err := hubregistry.Load(context.Background(), hub, hubregistry.LoadOptions{CacheDir: cache})
	require.NoError(t, err)
	// Materialize both skill dirs first so the orphan detector has
	// directories to walk.
	_, err = reg.FetchComponent(context.Background(), "design-system", hubregistry.KindSkill)
	require.NoError(t, err)
	_, err = reg.FetchComponent(context.Background(), "code-review", hubregistry.KindSkill)
	require.NoError(t, err)
	// Drop one component so the existing dir becomes an orphan.
	reg.Components = reg.Components[:1]
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
