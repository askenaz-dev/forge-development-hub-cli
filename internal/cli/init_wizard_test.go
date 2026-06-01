package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/pkg/adapters"
	"github.com/forge/fdh/pkg/hubregistry"
)

const wizardFixtureRegistry = `schema_version: 1
skills:
  - name: design-system
    path: skills/design-system
    default: true
    agents_supported: [claude-code, codex]
    description: "Shared design tokens"
  - name: code-review
    path: skills/code-review
    default: false
    agents_supported: [claude-code, copilot]
    description: "PR review playbook"
`

// buildWizardHub stages a git-backed hub fixture so Load can read
// HEAD without any network. Mirrors pkg/hubregistry's test helper
// (duplicated to keep the CLI package decoupled from that one's
// internals).
func buildWizardHub(t *testing.T) string {
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
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "skills", "design-system"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "skills", "code-review"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "skills", "registry.yaml"), []byte(wizardFixtureRegistry), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "skills", "design-system", "SKILL.md"), []byte("# Design\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "skills", "code-review", "SKILL.md"), []byte("# Review\n"), 0o644))
	runGit("add", "-A")
	runGit("commit", "-m", "fixture")
	return dir
}

// fakePrompter is a deterministic wizardPrompter for tests.
//
// Field semantics:
//   - pickedHarness: returned by SelectHarness; "" mimics "skip step".
//   - pickedAgents:  returned by SelectAgents.
//   - pickedComponents: returned by SelectComponents (new kind-aware step).
//   - pickedSkills:  legacy field; if set AND pickedComponents is empty,
//                    forwarded as componentRef{kind=skill} so old tests
//                    keep matching during the migration.
//   - confirm:       Step 3 yes/no.
type fakePrompter struct {
	pickedHarness    string
	pickedAgents     []string
	pickedComponents []componentRef
	pickedSkills     []string
	confirm          bool
}

func (f fakePrompter) SelectHarness(_ []harnessChoice, defaultPick string) (string, error) {
	if f.pickedHarness != "" {
		return f.pickedHarness, nil
	}
	return defaultPick, nil
}
func (f fakePrompter) SelectAgents(_ []string) ([]string, error) {
	return f.pickedAgents, nil
}
func (f fakePrompter) SelectComponents(_ []componentChoice, _ []componentRef) ([]componentRef, error) {
	if len(f.pickedComponents) > 0 {
		return f.pickedComponents, nil
	}
	out := make([]componentRef, 0, len(f.pickedSkills))
	for _, s := range f.pickedSkills {
		out = append(out, componentRef{Name: s, Kind: "skill"})
	}
	return out, nil
}
func (f fakePrompter) SelectSkills(_, _ []skillChoice, _ []string) ([]string, error) {
	return f.pickedSkills, nil
}
func (f fakePrompter) Confirm(_ string) (bool, error) { return f.confirm, nil }

// buildWizardRC fakes a runContext where Claude Code is "detected"
// by creating ~/.claude (the cheapest probe in the builtin manifest).
func buildWizardRC(t *testing.T) *runContext {
	t.Helper()
	home := t.TempDir()
	project := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o755))

	mani, err := adapters.LoadDefault()
	require.NoError(t, err)
	return &runContext{
		Ctx:          context.Background(),
		HomeDir:      home,
		ProjectRoot:  project,
		Adapters:     mani,
		BuildVersion: "0.0.0-test",
	}
}

func TestRunInitWizard_InteractiveHappyPath(t *testing.T) {
	hub := buildWizardHub(t)
	rc := buildWizardRC(t)

	prompter := fakePrompter{
		pickedAgents: []string{"claude-code"},
		pickedSkills: []string{"design-system"},
		confirm:      true,
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
	assert.Equal(t, []string{"design-system"}, skills)
	require.Len(t, installed, 1)
	assert.Equal(t, "design-system", installed[0].Skill)
	assert.Equal(t, "claude-code", installed[0].Agent)
	assert.NotEmpty(t, installed[0].ContentHash)

	target := filepath.Join(rc.ProjectRoot, ".claude", "skills", "design-system", "SKILL.md")
	_, err = os.Stat(target)
	assert.NoError(t, err)
}

func TestRunInitWizard_UserCancels(t *testing.T) {
	hub := buildWizardHub(t)
	rc := buildWizardRC(t)
	prompter := fakePrompter{
		pickedAgents: []string{"claude-code"},
		pickedSkills: []string{"design-system"},
		confirm:      false,
	}
	var stdout bytes.Buffer
	agents, skills, installed, err := runInitWizard(
		context.Background(),
		&stdout, io.Discard,
		rc, hub, prompter,
		wizardInput{},
		false, false, "0.0.0-test",
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"claude-code"}, agents)
	assert.Equal(t, []string{"design-system"}, skills)
	assert.Empty(t, installed)
	assert.Contains(t, stdout.String(), "Canceled")
}

func TestRunInitWizard_NonInteractiveFromFlags(t *testing.T) {
	hub := buildWizardHub(t)
	rc := buildWizardRC(t)

	agents, skills, installed, err := runInitWizard(
		context.Background(),
		io.Discard, io.Discard,
		rc, hub, nil, // no prompter ⇒ non-interactive
		wizardInput{
			Agents: []string{"claude-code"},
			Skills: []string{"design-system"},
		},
		false, false, "0.0.0-test",
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"claude-code"}, agents)
	assert.Equal(t, []string{"design-system"}, skills)
	require.Len(t, installed, 1)
	assert.False(t, installed[0].Skipped)
}

func TestRunInitWizard_DryRunDoesNotTouchFilesystem(t *testing.T) {
	hub := buildWizardHub(t)
	rc := buildWizardRC(t)

	_, _, installed, err := runInitWizard(
		context.Background(),
		io.Discard, io.Discard,
		rc, hub, nil,
		wizardInput{Agents: []string{"claude-code"}, Skills: []string{"design-system"}},
		false, true, "0.0.0-test",
	)
	require.NoError(t, err)
	require.Len(t, installed, 1)
	_, err = os.Stat(filepath.Join(rc.ProjectRoot, ".claude", "skills", "design-system", "SKILL.md"))
	assert.True(t, os.IsNotExist(err), "dry-run must not write skill files")
}

func TestRunInitWizard_NonInteractiveDefaultsInstallsAllDefaults(t *testing.T) {
	hub := buildWizardHub(t)
	rc := buildWizardRC(t)
	_, skills, installed, err := runInitWizard(
		context.Background(),
		io.Discard, io.Discard,
		rc, hub, nil,
		wizardInput{},
		false, false, "0.0.0-test",
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"design-system"}, skills, "design-system is the only default in the fixture")
	assert.NotEmpty(t, installed)
}

func TestRunInitWizard_NoDefaultsSkipsThem(t *testing.T) {
	hub := buildWizardHub(t)
	rc := buildWizardRC(t)
	_, skills, installed, err := runInitWizard(
		context.Background(),
		io.Discard, io.Discard,
		rc, hub, nil,
		wizardInput{},
		true, false, "0.0.0-test",
	)
	require.NoError(t, err)
	assert.Empty(t, skills)
	assert.Empty(t, installed)
}

func TestRunInitWizard_UnknownSkillFails(t *testing.T) {
	hub := buildWizardHub(t)
	rc := buildWizardRC(t)
	_, _, _, err := runInitWizard(
		context.Background(),
		io.Discard, io.Discard,
		rc, hub, nil,
		wizardInput{Agents: []string{"claude-code"}, Skills: []string{"no-such-skill"}},
		false, false, "0.0.0-test",
	)
	require.Error(t, err)
	assert.Equal(t, ExitInvalidUsage, ExitCode(err))
}

func TestRunInitWizard_NoAgentsDetectedFails(t *testing.T) {
	hub := buildWizardHub(t)
	rc := buildWizardRC(t)
	// Swap the adapter manifest for one whose only probe targets a
	// path under a brand-new tempdir — guaranteed not to exist, and
	// independent of whatever's on the test machine's PATH.
	rc.Adapters = &adapters.Manifest{
		Agents: []adapters.AgentEntry{{
			ID:          "claude-code",
			DisplayName: "Claude Code",
			Detect: []adapters.Probe{
				{Type: adapters.ProbeDirExists, Path: filepath.Join(t.TempDir(), "definitely-does-not-exist")},
			},
			Paths: adapters.ScopedPaths{User: []string{"~/.claude/skills/<name>/"}},
		}},
	}
	_, _, _, err := runInitWizard(
		context.Background(),
		io.Discard, io.Discard,
		rc, hub, nil,
		wizardInput{},
		false, false, "0.0.0-test",
	)
	require.Error(t, err)
	assert.Equal(t, ExitNoAgent, ExitCode(err))
}

func TestHubURLFromConfigOrFlag_NilRegistry(t *testing.T) {
	rc := &runContext{}
	assert.Empty(t, hubURLFromConfigOrFlag(rc))
}

func TestSplitDefaultsAndExtras_AgentFilter(t *testing.T) {
	all := []hubregistry.ComponentEntry{
		{Name: "a", Kind: hubregistry.KindSkill, Default: true, AgentsSupported: []string{"claude-code"}},
		{Name: "b", Kind: hubregistry.KindSkill, Default: false, AgentsSupported: []string{"copilot"}},
	}
	defaults, extras := splitDefaultsAndExtras(all, []string{"claude-code"})
	require.Len(t, defaults, 1)
	assert.Equal(t, "a", defaults[0].Name)
	assert.Empty(t, extras)
}

func TestResolveSkillSelection_StripsPlusAndFlagsUnknown(t *testing.T) {
	catalog := []hubregistry.ComponentEntry{
		{Name: "x", Kind: hubregistry.KindSkill}, {Name: "y", Kind: hubregistry.KindSkill},
	}
	valid, unknown := resolveSkillSelection([]string{"+x", "z"}, catalog)
	assert.Equal(t, []string{"x"}, valid)
	assert.Equal(t, []string{"z"}, unknown)
}
