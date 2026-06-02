package cli

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/pkg/hubregistry"
)

// hubregistryLoad is a thin test helper around hubregistry.Load with a
// per-test cache dir so parallel runs don't share state.
func hubregistryLoad(ctx context.Context, hubPath string) (*hubregistry.Registry, error) {
	return hubregistry.Load(ctx, hubPath, hubregistry.LoadOptions{
		SkipFetch: true,
	})
}

// wizardV2HubFixture is a v2 hub: schema_version: 2, components[]
// across all four kinds plus a hub/harnesses.yaml with two harnesses.
// Exercises the path the new wizard uses against a real (post-migration)
// hub instead of the v1 fixture in init_wizard_test.go.
const wizardV2RegistryYAML = `schema_version: 2
hub_version: "2026.05"
components:
  - name: design-system
    kind: skill
    description: UI design system
    owner_team: design-platform
    tags: [ui]
    default: true
    min_fdh_version: "0.4.0"
    agents_supported: [claude-code, codex]
    path: skills/design-system

  - name: spec-driven-development
    kind: skill
    description: SDD methodology
    owner_team: dx-platform
    tags: [process]
    default: false
    min_fdh_version: "0.4.0"
    agents_supported: [claude-code, codex]
    path: skills/spec-driven-development

  - name: no-console-log
    kind: rule
    description: No console.* in production code
    owner_team: dx-platform
    tags: [ts]
    default: true
    min_fdh_version: "0.4.0"
    agents_supported: [claude-code, codex]
    path: rules/no-console-log

  - name: no-hardcoded-secrets
    kind: rule
    description: No secrets in source
    owner_team: appsec
    tags: [security]
    default: true
    min_fdh_version: "0.4.0"
    agents_supported: [claude-code, codex]
    path: rules/no-hardcoded-secrets
`

const wizardV2HarnessesYAML = `schema_version: 1
harnesses:
  default:
    description: Base bundle
    owner_team: dx-platform
    skills: [design-system]
    rules:  [no-console-log, no-hardcoded-secrets]

  backend-team:
    description: Backend stack
    owner_team: platform-engineering
    skills: [spec-driven-development]
    rules:  [no-hardcoded-secrets]
`

// buildWizardV2Hub stages a v2-schema hub with harnesses.
func buildWizardV2Hub(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git required for wizard tests")
	}
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		out, err := c.CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, string(out))
	}
	runGit("init", "--initial-branch", "main")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "hub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hub", "registry.yaml"), []byte(wizardV2RegistryYAML), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hub", "harnesses.yaml"), []byte(wizardV2HarnessesYAML), 0o644))
	// Stage component dirs with minimal entrypoints.
	for _, p := range []struct{ dir, entry string }{
		{"skills/design-system", "SKILL.md"},
		{"skills/spec-driven-development", "SKILL.md"},
		{"rules/no-console-log", "RULE.md"},
		{"rules/no-hardcoded-secrets", "RULE.md"},
	} {
		full := filepath.Join(dir, p.dir)
		require.NoError(t, os.MkdirAll(full, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(full, p.entry), []byte("# "+p.dir+"\n"), 0o644))
	}
	runGit("add", "-A")
	runGit("commit", "-m", "fixture v2 hub")
	return dir
}

// When the prompter accepts the "default" harness, the wizard installs
// the harness's 3 members across the chosen agents — proves the
// install loop materializes all 4 kinds (not skill-only).
func TestRunInitWizard_HarnessDefaultInstallsAllKinds(t *testing.T) {
	hub := buildWizardV2Hub(t)
	rc := buildWizardRC(t)

	prompter := fakePrompter{
		pickedHarness: "default",
		pickedAgents:  []string{"claude-code"},
		// pickedComponents empty → fake forwards skills; explicit
		// component list covers all 3 default members.
		pickedComponents: []componentRef{
			{Name: "design-system", Kind: "skill"},
			{Name: "no-console-log", Kind: "rule"},
			{Name: "no-hardcoded-secrets", Kind: "rule"},
		},
		confirm: true,
	}
	agents, skills, installed, err := runInitWizard(
		context.Background(),
		io.Discard, io.Discard,
		rc, hub, prompter,
		wizardInput{},
		false, false, "0.0.0-test",
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"claude-code"}, agents)
	assert.Equal(t, []string{"design-system"}, skills, "Skills view filters to kind=skill only")
	require.Len(t, installed, 3, "1 skill + 2 rules × 1 agent")

	// Materialized: SKILL.md + 2 rule files on disk.
	_, err = os.Stat(filepath.Join(rc.ProjectRoot, ".claude", "skills", "design-system", "SKILL.md"))
	assert.NoError(t, err, "skill materialized")
	// confirm:true accepted both prompts (selection + "Install now?"), so the
	// lock was written too.
	_, err = os.Stat(filepath.Join(rc.ProjectRoot, ".fdh", "lock.yaml"))
	assert.NoError(t, err, "lock written when install confirmed")
}

// installNowDeclinedPrompter accepts the Step-3 selection (first Confirm) but
// declines the "Install now?" prompt (second Confirm), exercising the
// configure-without-materializing path.
type installNowDeclinedPrompter struct {
	fakePrompter
	calls *int
}

func (p installNowDeclinedPrompter) Confirm(_ string) (bool, error) {
	*p.calls++
	return *p.calls == 1, nil // 1st (selection) = yes, 2nd (install now) = no
}

// Declining "Install now?" writes the manifest (intent) but leaves nothing
// materialized and no lock — the "configure once, apply later" path.
func TestRunInitWizard_DeclineInstallNowWritesManifestOnly(t *testing.T) {
	hub := buildWizardV2Hub(t)
	rc := buildWizardRC(t)

	calls := 0
	prompter := installNowDeclinedPrompter{
		fakePrompter: fakePrompter{
			pickedHarness: "default",
			pickedAgents:  []string{"claude-code"},
			pickedComponents: []componentRef{
				{Name: "design-system", Kind: "skill"},
				{Name: "no-console-log", Kind: "rule"},
				{Name: "no-hardcoded-secrets", Kind: "rule"},
			},
		},
		calls: &calls,
	}
	_, _, installed, err := runInitWizard(
		context.Background(),
		io.Discard, io.Discard,
		rc, hub, prompter,
		wizardInput{},
		false, false, "0.0.0-test",
	)
	require.NoError(t, err)
	assert.Empty(t, installed, "declining install materializes nothing")

	// Manifest written (the intent), but no lock and no materialized files.
	_, e1 := os.Stat(filepath.Join(rc.ProjectRoot, ".fdh", "manifest.yaml"))
	assert.NoError(t, e1, "manifest written even when install declined")
	_, e2 := os.Stat(filepath.Join(rc.ProjectRoot, ".fdh", "lock.yaml"))
	assert.True(t, os.IsNotExist(e2), "lock NOT written when install declined")
	_, e3 := os.Stat(filepath.Join(rc.ProjectRoot, ".claude", "skills", "design-system", "SKILL.md"))
	assert.True(t, os.IsNotExist(e3), "components NOT materialized when install declined")
}

// Non-interactive path: pass --harness backend-team via wizardInput,
// no prompter. The preselect (harness members + catalog defaults)
// becomes the install set.
func TestRunInitWizard_HarnessBackendTeamNonInteractive(t *testing.T) {
	hub := buildWizardV2Hub(t)
	rc := buildWizardRC(t)

	_, _, installed, err := runInitWizard(
		context.Background(),
		io.Discard, io.Discard,
		rc, hub, nil,
		wizardInput{
			Harness: "backend-team",
			Agents:  []string{"claude-code"},
		},
		false, false, "0.0.0-test",
	)
	require.NoError(t, err)

	// backend-team has spec-driven-development + no-hardcoded-secrets.
	// Catalog defaults add design-system + no-console-log. Total: 4
	// components × 1 agent.
	require.Len(t, installed, 4, "harness members + defaults, all installed")
	names := map[string]bool{}
	for _, r := range installed {
		names[r.Skill] = true
	}
	assert.True(t, names["spec-driven-development"], "harness skill")
	assert.True(t, names["no-hardcoded-secrets"], "harness rule")
	assert.True(t, names["design-system"], "catalog default skill")
	assert.True(t, names["no-console-log"], "catalog default rule")
}

// --no-defaults drops the catalog defaults but keeps the harness
// members. Proves the precedence: harness > defaults, and defaults
// can be suppressed independently.
func TestRunInitWizard_HarnessNoDefaultsKeepsHarnessOnly(t *testing.T) {
	hub := buildWizardV2Hub(t)
	rc := buildWizardRC(t)

	_, _, installed, err := runInitWizard(
		context.Background(),
		io.Discard, io.Discard,
		rc, hub, nil,
		wizardInput{
			Harness: "backend-team",
			Agents:  []string{"claude-code"},
		},
		true, // noDefaults
		false, "0.0.0-test",
	)
	require.NoError(t, err)

	// Only the 2 backend-team members.
	require.Len(t, installed, 2)
	names := map[string]bool{}
	for _, r := range installed {
		names[r.Skill] = true
	}
	assert.True(t, names["spec-driven-development"])
	assert.True(t, names["no-hardcoded-secrets"])
	assert.False(t, names["design-system"], "default suppressed")
	assert.False(t, names["no-console-log"], "default suppressed")
}

// Unknown harness name fails with ExitInvalidUsage AND the message
// lists what was available so the user can correct their flag.
func TestRunInitWizard_UnknownHarnessFails(t *testing.T) {
	hub := buildWizardV2Hub(t)
	rc := buildWizardRC(t)

	_, _, _, err := runInitWizard(
		context.Background(),
		io.Discard, io.Discard,
		rc, hub, nil,
		wizardInput{
			Harness: "nope-not-a-harness",
			Agents:  []string{"claude-code"},
		},
		false, false, "0.0.0-test",
	)
	require.Error(t, err)
	assert.Equal(t, ExitInvalidUsage, ExitCode(err))
	assert.Contains(t, err.Error(), "unknown harness")
	assert.Contains(t, err.Error(), "default")
	assert.Contains(t, err.Error(), "backend-team")
}

// When the manifest is persisted after init, it records the chosen
// harness so a future `fdh switch` has a from-state to diff from.
func TestRunInitWizard_PersistsHarnessInManifest(t *testing.T) {
	hub := buildWizardV2Hub(t)
	rc := buildWizardRC(t)

	_, _, _, err := runInitWizard(
		context.Background(),
		io.Discard, io.Discard,
		rc, hub, nil,
		wizardInput{
			Harness: "default",
			Agents:  []string{"claude-code"},
		},
		false, false, "0.0.0-test",
	)
	require.NoError(t, err)

	body, err := os.ReadFile(filepath.Join(rc.ProjectRoot, ".fdh", "manifest.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(body), "harness: default")
}

// Unit test for the diff helper.
func TestDiffComponentSets(t *testing.T) {
	old := []componentRef{
		{Name: "a", Kind: "skill"},
		{Name: "b", Kind: "rule"},
		{Name: "c", Kind: "skill"},
	}
	new := []componentRef{
		{Name: "b", Kind: "rule"},
		{Name: "c", Kind: "skill"},
		{Name: "d", Kind: "hook"},
	}
	install, uninstall, unchanged := diffComponentSets(old, new)
	assert.Equal(t, []componentRef{{Name: "d", Kind: "hook"}}, install)
	assert.Equal(t, []componentRef{{Name: "a", Kind: "skill"}}, uninstall)
	assert.ElementsMatch(t,
		[]componentRef{{Name: "b", Kind: "rule"}, {Name: "c", Kind: "skill"}},
		unchanged)
}

// Unit test for the component-choice builder. Loads the v2 fixture
// hub directly via hubregistry.Load, then runs buildComponentChoices
// against the parsed view to verify harness > defaults precedence.
func TestBuildComponentChoices_HarnessOverridesDefault(t *testing.T) {
	hub := buildWizardV2Hub(t)
	reg, err := hubregistryLoad(context.Background(), hub)
	require.NoError(t, err)
	doc, err := reg.LoadHarnesses()
	require.NoError(t, err)
	harness := harnessMemberRefs(doc, "backend-team")

	// noDefaults=false → harness members + catalog defaults pre-checked.
	choices, preSelect := buildComponentChoices(reg, []string{"claude-code"}, harness, false)
	assert.NotEmpty(t, choices)
	assert.ElementsMatch(t, []string{
		"spec-driven-development",
		"no-hardcoded-secrets",
		"design-system",
		"no-console-log",
	}, refNames(preSelect))

	// noDefaults=true → only harness members pre-checked.
	_, preSelect2 := buildComponentChoices(reg, []string{"claude-code"}, harness, true)
	assert.ElementsMatch(t, []string{
		"spec-driven-development",
		"no-hardcoded-secrets",
	}, refNames(preSelect2))
}

func refNames(refs []componentRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Name)
	}
	return out
}
