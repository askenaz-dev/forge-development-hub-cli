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

	"github.com/forge/fdh/pkg/adapters"
)

// installFixtureSkill runs the wizard non-interactively to lay a
// skill on disk so the update tests have something to plan against.
func installFixtureSkill(t *testing.T, hub string, rc *runContext, skill string) {
	t.Helper()
	_, _, installed, err := runInitWizard(
		context.Background(),
		io.Discard, io.Discard,
		rc, hub, nil,
		wizardInput{Agents: []string{"claude-code"}, Skills: []string{skill}},
		false, false, "0.0.0-test",
	)
	require.NoError(t, err)
	require.NotEmpty(t, installed)
}

func TestFindInstalledSkills_FindsClaudeMarker(t *testing.T) {
	hub := buildWizardHub(t)
	rc := buildWizardRC(t)
	installFixtureSkill(t, hub, rc, "design-system")

	found, err := findInstalledSkills(rc.HomeDir, rc.ProjectRoot)
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "claude-code", found[0].Agent)
	assert.Equal(t, "design-system", found[0].Skill)
	assert.NotEmpty(t, found[0].Marker.ContentHash)
}

func TestPlanUpdates_UpToDateWhenHubUnchanged(t *testing.T) {
	hub := buildWizardHub(t)
	rc := buildWizardRC(t)
	installFixtureSkill(t, hub, rc, "design-system")

	installed, err := findInstalledSkills(rc.HomeDir, rc.ProjectRoot)
	require.NoError(t, err)

	reg, err := loadHubWithRecovery(context.Background(), io.Discard, hub, false)
	require.NoError(t, err)

	plan, err := planUpdates(context.Background(), installed, reg, nil, nil, false)
	require.NoError(t, err)
	require.Len(t, plan, 1)
	assert.Equal(t, "up-to-date", plan[0].Action)
}

func TestPlanUpdates_DriftDetectedWhenLocalEdited(t *testing.T) {
	hub := buildWizardHub(t)
	rc := buildWizardRC(t)
	installFixtureSkill(t, hub, rc, "design-system")

	// Tamper with the installed file.
	installedFile := filepath.Join(rc.ProjectRoot, ".claude", "skills", "design-system", "SKILL.md")
	require.NoError(t, os.WriteFile(installedFile, []byte("# DRIFT\n"), 0o644))

	installed, err := findInstalledSkills(rc.HomeDir, rc.ProjectRoot)
	require.NoError(t, err)
	reg, err := loadHubWithRecovery(context.Background(), io.Discard, hub, false)
	require.NoError(t, err)

	plan, err := planUpdates(context.Background(), installed, reg, nil, nil, false)
	require.NoError(t, err)
	require.Len(t, plan, 1)
	assert.Equal(t, "drift", plan[0].Action)
}

func TestPlanUpdates_ForceOverridesDrift(t *testing.T) {
	hub := buildWizardHub(t)
	rc := buildWizardRC(t)
	installFixtureSkill(t, hub, rc, "design-system")

	// Tamper + advance hub commit (re-commit the hub).
	installedFile := filepath.Join(rc.ProjectRoot, ".claude", "skills", "design-system", "SKILL.md")
	require.NoError(t, os.WriteFile(installedFile, []byte("# DRIFT\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(hub, "skills", "design-system", "SKILL.md"), []byte("# NEW\n"), 0o644))
	mustGitInDir(t, hub, "add", "-A")
	mustGitInDir(t, hub, "commit", "-m", "bump")

	installed, err := findInstalledSkills(rc.HomeDir, rc.ProjectRoot)
	require.NoError(t, err)
	reg, err := loadHubWithRecovery(context.Background(), io.Discard, hub, false)
	require.NoError(t, err)

	plan, err := planUpdates(context.Background(), installed, reg, nil, nil, true /* force */)
	require.NoError(t, err)
	require.Len(t, plan, 1)
	assert.Equal(t, "refresh", plan[0].Action)
}

func TestPlanUpdates_RefreshWhenHubMoved(t *testing.T) {
	hub := buildWizardHub(t)
	rc := buildWizardRC(t)
	installFixtureSkill(t, hub, rc, "design-system")

	// Move the hub forward.
	require.NoError(t, os.WriteFile(filepath.Join(hub, "skills", "design-system", "SKILL.md"), []byte("# NEW\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(hub, "skills", "design-system", "extra.md"), []byte("extra\n"), 0o644))
	mustGitInDir(t, hub, "add", "-A")
	mustGitInDir(t, hub, "commit", "-m", "bump")

	installed, err := findInstalledSkills(rc.HomeDir, rc.ProjectRoot)
	require.NoError(t, err)
	reg, err := loadHubWithRecovery(context.Background(), io.Discard, hub, false)
	require.NoError(t, err)

	plan, err := planUpdates(context.Background(), installed, reg, nil, nil, false)
	require.NoError(t, err)
	require.Len(t, plan, 1)
	assert.Equal(t, "refresh", plan[0].Action)
	assert.Contains(t, plan[0].Files.Modified, "SKILL.md")
	assert.Contains(t, plan[0].Files.Added, "extra.md")
}

func TestPlanUpdates_VanishedWhenHubDroppedSkill(t *testing.T) {
	hub := buildWizardHub(t)
	rc := buildWizardRC(t)
	installFixtureSkill(t, hub, rc, "design-system")

	// Edit the hub registry to drop design-system.
	newReg := `schema_version: 1
skills:
  - name: code-review
    path: skills/code-review
    agents_supported: [claude-code]
`
	require.NoError(t, os.WriteFile(filepath.Join(hub, "skills", "registry.yaml"), []byte(newReg), 0o644))
	mustGitInDir(t, hub, "add", "-A")
	mustGitInDir(t, hub, "commit", "-m", "drop design-system")

	installed, err := findInstalledSkills(rc.HomeDir, rc.ProjectRoot)
	require.NoError(t, err)
	reg, err := loadHubWithRecovery(context.Background(), io.Discard, hub, false)
	require.NoError(t, err)

	plan, err := planUpdates(context.Background(), installed, reg, nil, nil, false)
	require.NoError(t, err)
	require.Len(t, plan, 1)
	assert.Equal(t, "vanished", plan[0].Action)
}

func TestPlanUpdates_FiltersBySkillAndAgent(t *testing.T) {
	hub := buildWizardHub(t)
	rc := buildWizardRC(t)
	installFixtureSkill(t, hub, rc, "design-system")
	installFixtureSkill(t, hub, rc, "code-review")

	installed, err := findInstalledSkills(rc.HomeDir, rc.ProjectRoot)
	require.NoError(t, err)
	reg, err := loadHubWithRecovery(context.Background(), io.Discard, hub, false)
	require.NoError(t, err)

	plan, err := planUpdates(context.Background(), installed, reg,
		flagSetToMap([]string{"design-system"}),
		nil,
		false,
	)
	require.NoError(t, err)
	require.Len(t, plan, 1)
	assert.Equal(t, "design-system", plan[0].Skill)
}

func TestApplyUpdate_RefreshesContent(t *testing.T) {
	hub := buildWizardHub(t)
	rc := buildWizardRC(t)
	installFixtureSkill(t, hub, rc, "design-system")

	require.NoError(t, os.WriteFile(filepath.Join(hub, "skills", "design-system", "SKILL.md"), []byte("# NEW\n"), 0o644))
	mustGitInDir(t, hub, "add", "-A")
	mustGitInDir(t, hub, "commit", "-m", "bump")

	reg, err := loadHubWithRecovery(context.Background(), io.Discard, hub, false)
	require.NoError(t, err)
	res, err := applyUpdate(context.Background(), reg, "design-system", "claude-code", rc, "0.0.0-test", false)
	require.NoError(t, err)
	assert.False(t, res.Skipped)

	// Installed file now reflects the new content.
	body, err := os.ReadFile(filepath.Join(rc.ProjectRoot, ".claude", "skills", "design-system", "SKILL.md"))
	require.NoError(t, err)
	assert.Contains(t, string(body), "NEW")
}

func TestAdapterScopeRoot_ClaudeProject(t *testing.T) {
	root, err := adapterScopeRoot(adapters.ClaudeCodeAdapter{}, "/home", "/proj", adapters.ScopeProject)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/proj", ".claude", "skills"), root)
}

func TestAdapterScopeRoot_CopilotUser(t *testing.T) {
	root, err := adapterScopeRoot(adapters.CopilotAdapter{}, "/home", "", adapters.ScopeUser)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/home", ".config", "github-copilot", "prompts"), root)
}

// mustGitInDir runs git inline so the update tests can advance the
// hub commit between install and update.
func mustGitInDir(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, string(out))
}
