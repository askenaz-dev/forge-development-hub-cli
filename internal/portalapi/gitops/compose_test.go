package gitops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedRegistry / seedHarnesses are minimal-but-real catalog files matching the
// shipped hub schema, used to drive the edit composers against the fake client.
const seedRegistry = `schema_version: 2
hub_version: "2026.05"

components:
  - name: design-system
    kind: skill
    description: Forge design system.
    owner_team: design-platform
    tags: [ui]
    default: true
    min_fdh_version: "0.4.0"
    agents_supported: [claude-code, codex, copilot, opencode]
    path: skills/design-system

  - name: tech-stack
    kind: skill
    description: Approved stack.
    owner_team: dx-platform
    tags: [stack]
    default: false
    min_fdh_version: "0.4.0"
    agents_supported: [claude-code, codex, copilot, opencode]
    path: skills/tech-stack
`

const seedHarnesses = `schema_version: 1

harnesses:
  default:
    description: "Base harness."
    owner_team: dx-platform
    skills: [design-system]
    rules: [no-console-log]
    agents: [forge-pr-writer]
    hooks: [doctor-on-session-start]

  frontend-team:
    description: "Frontend."
    owner_team: design-platform
    skills: [design-system]
    rules: [no-console-log]
`

// writeValidSkillBundle creates a minimal, spec-valid skill bundle named `name`
// under a fresh temp dir and returns the bundle dir. The directory name equals
// the frontmatter name (bundle.Validate requires it).
func writeValidSkillBundle(t *testing.T, name, desc string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "references"), 0o755))
	skill := "---\nname: " + name + "\nversion: 0.1.0\ndescription: " + desc + "\n---\n\n# " + name + "\n\nBody.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skill), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "references", "guide.md"), []byte("# Guide\n"), 0o644))
	return dir
}

func seedEditFakes() *fakeClient {
	f := newFakeClient()
	f.setFile(hubRegistryPath, seedRegistry)
	f.setFile(hubHarnessesPath, seedHarnesses)
	return f
}

var testRequestor = Requestor{Name: "Dev Eloper", Email: "dev@example.com", Role: "author"}

// --- import ---------------------------------------------------------------

func TestComposeImport_DeterministicBranchAndNonDefaultEntry(t *testing.T) {
	f := seedEditFakes()
	dir := writeValidSkillBundle(t, "card-grid", "A grid of cards. Use when laying out cards.")

	res, err := ComposeImport(context.Background(), f, "skill", "card-grid", dir,
		ImportMeta{OwnerTeam: "design-platform", Agents: []string{"claude-code", "codex"}}, testRequestor, nil)
	require.NoError(t, err)

	assert.Equal(t, "web/import/skill/card-grid", res.Branch, "deterministic import branch")
	assert.False(t, res.AlreadyOpen)
	require.Len(t, f.openedPRs, 1, "exactly one PR opened")
	assert.Contains(t, f.openedPRs[0].url, "/pull/")

	// Commit message mirrors the CLI conventional scope.
	require.Len(t, f.commits, 1)
	assert.Equal(t, "feat(card-grid): add skill", f.commits[0].message)

	// The bundle tree landed under skills/card-grid/.
	if _, ok := f.lastCommitFile("skills/card-grid/SKILL.md"); !ok {
		t.Fatalf("expected skills/card-grid/SKILL.md in the commit; paths=%v", f.committedPaths())
	}
	assert.Contains(t, f.committedPaths(), "skills/card-grid/references/guide.md")

	// The registry entry is appended with default:false and agents_supported.
	reg, ok := f.lastCommitFile(hubRegistryPath)
	require.True(t, ok)
	assert.Contains(t, reg, "name: card-grid")
	assert.Contains(t, reg, "default: false", "imported component must be non-default")
	assert.Contains(t, reg, "agents_supported: [claude-code, codex]")
	assert.Contains(t, reg, "path: skills/card-grid")

	// PR body credits the requestor + role and states the propose-only note.
	body := f.openedPRs[0].body
	assert.Contains(t, body, "Dev Eloper")
	assert.Contains(t, body, "author")
	assert.Contains(t, body, proposeFooter)
}

func TestComposeImport_AgentsDefaultWhenUnset(t *testing.T) {
	f := seedEditFakes()
	dir := writeValidSkillBundle(t, "card-grid", "A grid of cards.")
	_, err := ComposeImport(context.Background(), f, "skill", "card-grid", dir, ImportMeta{}, testRequestor, nil)
	require.NoError(t, err)
	reg, _ := f.lastCommitFile(hubRegistryPath)
	// Empty agents → the per-kind skill default (mirrors registryEntryYAML).
	assert.Contains(t, reg, "agents_supported: [claude-code, codex, copilot, opencode]")
}

func TestComposeImport_NameCollisionAbortsBeforePush(t *testing.T) {
	f := seedEditFakes()
	// tech-stack already exists in the seed registry.
	dir := writeValidSkillBundle(t, "tech-stack", "Approved stack.")
	_, err := ComposeImport(context.Background(), f, "skill", "tech-stack", dir, ImportMeta{}, testRequestor, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNameCollision)
	assert.Empty(t, f.commits, "no commit on a name collision")
	assert.Empty(t, f.openedPRs, "no PR on a name collision")
}

func TestComposeImport_DirCollisionAbortsBeforePush(t *testing.T) {
	f := seedEditFakes()
	// A directory exists in the hub even though the registry entry does not.
	f.setFile("skills/card-grid/SKILL.md", "---\nname: card-grid\n---\n")
	dir := writeValidSkillBundle(t, "card-grid", "A grid of cards.")
	_, err := ComposeImport(context.Background(), f, "skill", "card-grid", dir, ImportMeta{}, testRequestor, nil)
	require.ErrorIs(t, err, ErrNameCollision)
	assert.Empty(t, f.commits)
	assert.Empty(t, f.openedPRs)
}

func TestComposeImport_ValidationFailsAbortsBeforePush(t *testing.T) {
	f := seedEditFakes()
	// A bundle whose directory name does not match the frontmatter name fails
	// Bundle.Validate — before any push.
	root := t.TempDir()
	dir := filepath.Join(root, "wrongdir")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: card-grid\nversion: 0.1.0\ndescription: x\n---\n# x\n"), 0o644))

	_, err := ComposeImport(context.Background(), f, "skill", "card-grid", dir, ImportMeta{}, testRequestor, nil)
	require.Error(t, err)
	var ve *ErrValidation
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "validate", ve.Check)
	assert.Empty(t, f.commits, "validation failure aborts before any push")
	assert.Empty(t, f.openedPRs)
	// And it never even queried for an existing PR (gate is first).
	assert.NotContains(t, f.calls, "CommitFiles")
}

func TestComposeImport_ScanFailAbortsBeforePush(t *testing.T) {
	f := seedEditFakes()
	dir := writeValidSkillBundle(t, "leaky", "Leaks a secret. Use when testing the scanner.")
	// Plant a blocking secret pattern the fdh-scan detectors flag as `error`.
	// A GitHub PAT-shaped token (gh*_ + 36+ chars) trips secret/github-token,
	// and carries no EXAMPLE marker so the documented-example allowlist does not
	// suppress it.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.txt"),
		[]byte("token = ghp_0123456789abcdefghijklmnopqrstuvwxyzABCD\n"), 0o644))
	_, err := ComposeImport(context.Background(), f, "skill", "leaky", dir, ImportMeta{}, testRequestor, nil)
	require.Error(t, err)
	var ve *ErrValidation
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "scan", ve.Check)
	assert.Empty(t, f.commits)
	assert.Empty(t, f.openedPRs)
}

func TestComposeImport_IdempotentReturnsExistingPR(t *testing.T) {
	f := seedEditFakes()
	f.existingOpenPR["web/import/skill/card-grid"] = "https://github.com/askenaz-dev/forge-development-hub/pull/42"
	dir := writeValidSkillBundle(t, "card-grid", "A grid of cards.")

	res, err := ComposeImport(context.Background(), f, "skill", "card-grid", dir, ImportMeta{}, testRequestor, nil)
	require.NoError(t, err)
	assert.True(t, res.AlreadyOpen)
	assert.Equal(t, "https://github.com/askenaz-dev/forge-development-hub/pull/42", res.URL)
	assert.Empty(t, f.commits, "idempotent action opens no duplicate")
	assert.Empty(t, f.openedPRs)
}

// --- harness --------------------------------------------------------------

func TestComposeHarness_TouchesOnlyHarnessesYAML(t *testing.T) {
	f := seedEditFakes()
	desc := "Updated frontend harness."
	edit := HarnessEdit{
		Description: &desc,
		AddRules:    []string{"no-any-cast"},
	}
	res, err := ComposeHarness(context.Background(), f, "frontend-team", edit, testRequestor)
	require.NoError(t, err)
	assert.Equal(t, "web/harness/frontend-team", res.Branch)
	require.Len(t, f.commits, 1)
	assert.Equal(t, "chore(harness): update frontend-team", f.commits[0].message)

	// ONLY harnesses.yaml changed.
	paths := f.committedPaths()
	require.Len(t, paths, 1, "harness edit must touch exactly one file; got %v", paths)
	assert.Equal(t, hubHarnessesPath, paths[0])

	h, _ := f.lastCommitFile(hubHarnessesPath)
	assert.Contains(t, h, "no-any-cast", "added rule present")
	assert.Contains(t, h, "Updated frontend harness.", "description replaced")
}

func TestComposeHarness_RemoveComponent(t *testing.T) {
	f := seedEditFakes()
	edit := HarnessEdit{RemoveSkills: []string{"design-system"}}
	_, err := ComposeHarness(context.Background(), f, "frontend-team", edit, testRequestor)
	require.NoError(t, err)
	h, _ := f.lastCommitFile(hubHarnessesPath)
	// design-system removed from frontend-team but NOT from default.
	frontend := h[strings.Index(h, "frontend-team:"):]
	assert.NotContains(t, frontend, "design-system")
}

func TestComposeHarness_IdempotentReturnsExistingPR(t *testing.T) {
	f := seedEditFakes()
	f.existingOpenPR["web/harness/frontend-team"] = "https://github.com/askenaz-dev/forge-development-hub/pull/7"
	_, err := ComposeHarness(context.Background(), f, "frontend-team", HarnessEdit{AddRules: []string{"no-any-cast"}}, testRequestor)
	require.NoError(t, err)
	assert.Empty(t, f.commits)
	assert.Empty(t, f.openedPRs)
}

// --- curate ---------------------------------------------------------------

func TestComposeCurate_SetDefaultTrueSyncsHarnessAtomically(t *testing.T) {
	f := seedEditFakes()
	yes := true
	res, err := ComposeCurate(context.Background(), f, "skill", "tech-stack", CurateAction{SetDefault: &yes}, testRequestor)
	require.NoError(t, err)
	assert.Equal(t, "web/curate/skill/tech-stack", res.Branch)

	require.Len(t, f.commits, 1, "default sync must be ONE atomic commit")
	paths := f.committedPaths()
	assert.Contains(t, paths, hubRegistryPath)
	assert.Contains(t, paths, hubHarnessesPath, "default change must edit the default harness in the SAME commit (D6)")
	assert.Len(t, paths, 2)
	assert.Equal(t, "chore(tech-stack): set default true", f.commits[0].message)

	reg, _ := f.lastCommitFile(hubRegistryPath)
	// tech-stack flipped to default:true; design-system unchanged.
	techIdx := strings.Index(reg, "name: tech-stack")
	require.GreaterOrEqual(t, techIdx, 0)
	techBlock := reg[techIdx:]
	assert.Contains(t, techBlock[:strings.Index(techBlock, "path:")], "default: true")

	h, _ := f.lastCommitFile(hubHarnessesPath)
	defBlock := h[strings.Index(h, "default:"):strings.Index(h, "frontend-team:")]
	assert.Contains(t, defBlock, "tech-stack", "tech-stack added to the default harness skills")
}

func TestComposeCurate_SetDefaultFalseRemovesFromHarness(t *testing.T) {
	f := seedEditFakes()
	no := false
	// design-system starts default:true and is in the default harness.
	res, err := ComposeCurate(context.Background(), f, "skill", "design-system", CurateAction{SetDefault: &no}, testRequestor)
	require.NoError(t, err)
	assert.Equal(t, "chore(design-system): set default false", f.commits[0].message)
	_ = res

	paths := f.committedPaths()
	assert.Contains(t, paths, hubRegistryPath)
	assert.Contains(t, paths, hubHarnessesPath)

	h, _ := f.lastCommitFile(hubHarnessesPath)
	defBlock := h[strings.Index(h, "default:"):strings.Index(h, "frontend-team:")]
	assert.NotContains(t, defBlock, "design-system", "design-system removed from the default harness")
}

func TestComposeCurate_DeprecateSetsVersionStatus(t *testing.T) {
	f := seedEditFakes()
	res, err := ComposeCurate(context.Background(), f, "skill", "design-system",
		CurateAction{Lifecycle: "deprecated", Version: "0.4.0"}, testRequestor)
	require.NoError(t, err)
	assert.Equal(t, "web/curate/skill/design-system", res.Branch)
	assert.Equal(t, "chore(design-system): deprecate@0.4.0", f.commits[0].message)

	// Lifecycle edits ONLY the registry (not the harness).
	paths := f.committedPaths()
	require.Len(t, paths, 1)
	assert.Equal(t, hubRegistryPath, paths[0])

	reg, _ := f.lastCommitFile(hubRegistryPath)
	assert.Contains(t, reg, "status: deprecated")
	assert.Contains(t, reg, "0.4.0")
}

func TestComposeCurate_YankAfterDeprecateAllowed(t *testing.T) {
	f := seedEditFakes()
	// First deprecate.
	_, err := ComposeCurate(context.Background(), f, "skill", "design-system",
		CurateAction{Lifecycle: "deprecated", Version: "0.4.0"}, testRequestor)
	require.NoError(t, err)
	// The fake reflects the commit back into the file store, so a follow-up
	// curate on a fresh branch sees status:deprecated and may move to yanked.
	f2 := newFakeClient()
	f2.setFile(hubRegistryPath, mustString(f.lastCommitFile(hubRegistryPath)))
	f2.setFile(hubHarnessesPath, seedHarnesses)
	_, err = ComposeCurate(context.Background(), f2, "skill", "design-system",
		CurateAction{Lifecycle: "yanked", Version: "0.4.0"}, testRequestor)
	require.NoError(t, err)
	reg, _ := f2.lastCommitFile(hubRegistryPath)
	assert.Contains(t, reg, "status: yanked")
}

func TestComposeCurate_UnYankRejected(t *testing.T) {
	f := seedEditFakes()
	// Seed a yanked version on design-system.
	reg, _, err := setVersionStatus([]byte(seedRegistry), "skill", "design-system", "0.4.0", "yanked")
	require.NoError(t, err)
	f.setFile(hubRegistryPath, string(reg))

	// Attempt to move it back to active via deprecate (a backward move) — and
	// also a direct attempt is impossible since the API never offers "active".
	_, err = ComposeCurate(context.Background(), f, "skill", "design-system",
		CurateAction{Lifecycle: "deprecated", Version: "0.4.0"}, testRequestor)
	require.Error(t, err)
	var le *ErrLifecycle
	require.ErrorAs(t, err, &le)
	assert.Empty(t, f.commits, "un-yank/backward transition opens no PR")
	assert.Empty(t, f.openedPRs)
}

func TestComposeCurate_IdempotentReturnsExistingPR(t *testing.T) {
	f := seedEditFakes()
	f.existingOpenPR["web/curate/skill/tech-stack"] = "https://github.com/askenaz-dev/forge-development-hub/pull/9"
	yes := true
	res, err := ComposeCurate(context.Background(), f, "skill", "tech-stack", CurateAction{SetDefault: &yes}, testRequestor)
	require.NoError(t, err)
	assert.True(t, res.AlreadyOpen)
	assert.Empty(t, f.commits)
	assert.Empty(t, f.openedPRs)
}

// --- disabled client ------------------------------------------------------

func TestComposers_DisabledClientReturnsTypedError(t *testing.T) {
	d := Disabled()
	dir := writeValidSkillBundle(t, "card-grid", "A grid.")
	_, err := ComposeImport(context.Background(), d, "skill", "card-grid", dir, ImportMeta{}, testRequestor, nil)
	assert.ErrorIs(t, err, ErrGitopsNotConfigured)

	_, err = ComposeHarness(context.Background(), d, "default", HarnessEdit{AddRules: []string{"x"}}, testRequestor)
	assert.ErrorIs(t, err, ErrGitopsNotConfigured)

	yes := true
	_, err = ComposeCurate(context.Background(), d, "skill", "x", CurateAction{SetDefault: &yes}, testRequestor)
	assert.ErrorIs(t, err, ErrGitopsNotConfigured)
}

func mustString(s string, _ bool) string { return s }
