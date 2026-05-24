package adapters_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/forge/fdh/pkg/adapters"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeSourceSkill stages a fake hub skill directory with SKILL.md
// and an extra subresource so flat adapters can be tested for the
// warning path.
func makeSourceSkill(t *testing.T, name, body string, withSubresource bool) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644))
	if withSubresource {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "references"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "references", "extra.md"), []byte("# extra\n"), 0o644))
	}
	return dir
}

func defaultOpts(name, projectRoot, homeDir string) adapters.InstallOpts {
	return adapters.InstallOpts{
		SkillName:      name,
		ProjectRoot:    projectRoot,
		HomeDir:        homeDir,
		Scope:          adapters.ScopeProject,
		HubVersion:     "2026.05",
		HubCommit:      "abcd1234",
		InstalledByFDH: "0.5.2",
	}
}

func TestClaudeCodeAdapter_ProjectInstallWritesDirAndMarker(t *testing.T) {
	src := makeSourceSkill(t, "design-system", "---\nname: design-system\n---\n# DS\n", true)
	project := t.TempDir()

	a := adapters.ClaudeCodeAdapter{}
	res, err := a.Install(src, defaultOpts("design-system", project, ""))
	require.NoError(t, err)
	assert.Equal(t, "claude-code", res.Agent)
	assert.Equal(t, filepath.Join(project, ".claude", "skills", "design-system"), res.TargetPath)
	assert.NotEmpty(t, res.ContentHash)
	assert.Equal(t, filepath.Join(res.TargetPath, ".skill-version"), res.MarkerPath)

	// SKILL.md AND references/extra.md must have been copied.
	_, err = os.Stat(filepath.Join(res.TargetPath, "SKILL.md"))
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(res.TargetPath, "references", "extra.md"))
	assert.NoError(t, err)

	// Marker round-trips.
	marker, err := adapters.LoadMarker(res.MarkerPath)
	require.NoError(t, err)
	assert.Equal(t, "design-system", marker.Name)
	assert.Equal(t, "2026.05", marker.HubVersion)
	assert.Equal(t, "abcd1234", marker.HubCommit)
	assert.Equal(t, res.ContentHash, marker.ContentHash)
	assert.WithinDuration(t, time.Now(), marker.InstalledAt, 5*time.Minute)
}

func TestClaudeCodeAdapter_IdempotentSkipsWhenHashMatches(t *testing.T) {
	src := makeSourceSkill(t, "x", "---\nname: x\n---\nbody\n", false)
	project := t.TempDir()

	a := adapters.ClaudeCodeAdapter{}
	first, err := a.Install(src, defaultOpts("x", project, ""))
	require.NoError(t, err)
	assert.False(t, first.Skipped)

	second, err := a.Install(src, defaultOpts("x", project, ""))
	require.NoError(t, err)
	assert.True(t, second.Skipped, "second install with same content should skip")
	assert.Equal(t, first.ContentHash, second.ContentHash)
}

func TestClaudeCodeAdapter_DryRunDoesNotTouchFilesystem(t *testing.T) {
	src := makeSourceSkill(t, "x", "body\n", false)
	project := t.TempDir()

	opts := defaultOpts("x", project, "")
	opts.DryRun = true
	a := adapters.ClaudeCodeAdapter{}
	res, err := a.Install(src, opts)
	require.NoError(t, err)
	assert.NotEmpty(t, res.ContentHash)
	// Target dir should NOT exist after dry-run.
	_, err = os.Stat(res.TargetPath)
	assert.True(t, os.IsNotExist(err), "dry-run must not create the target directory")
}

func TestClaudeCodeAdapter_UserScopePath(t *testing.T) {
	home := t.TempDir()
	a := adapters.ClaudeCodeAdapter{}
	p, err := a.TargetPath("foo", "", home, adapters.ScopeUser)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".claude", "skills", "foo"), p)
}

func TestCodexAdapter_TargetPath(t *testing.T) {
	a := adapters.CodexAdapter{}
	p, err := a.TargetPath("foo", "/proj", "/home", adapters.ScopeProject)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/proj", ".codex", "skills", "foo"), p)
}

func TestCopilotAdapter_FlatInstallWritesPromptAndMarker(t *testing.T) {
	src := makeSourceSkill(t, "review", "---\nname: review\n---\n# Review\n", false)
	project := t.TempDir()

	a := adapters.CopilotAdapter{}
	res, err := a.Install(src, defaultOpts("review", project, ""))
	require.NoError(t, err)
	assert.Equal(t, "copilot", res.Agent)
	assert.Equal(t, filepath.Join(project, ".github", "prompts", "review.prompt.md"), res.TargetPath)
	assert.Equal(t, filepath.Join(project, ".github", "prompts", ".skill-version-review"), res.MarkerPath)

	body, err := os.ReadFile(res.TargetPath)
	require.NoError(t, err)
	assert.Contains(t, string(body), "# Review")
}

func TestCopilotAdapter_WarnsOnSubresources(t *testing.T) {
	src := makeSourceSkill(t, "review", "body\n", true)
	project := t.TempDir()

	a := adapters.CopilotAdapter{}
	res, err := a.Install(src, defaultOpts("review", project, ""))
	require.NoError(t, err)
	require.NotEmpty(t, res.Warnings)
	assert.Contains(t, res.Warnings[0], "not portable")
}

func TestOpenCodeAdapter_TargetPaths(t *testing.T) {
	a := adapters.OpenCodeAdapter{}
	got, err := a.TargetPath("x", "/proj", "/home", adapters.ScopeProject)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/proj", ".opencode", "commands", "x.md"), got)

	gotUser, err := a.TargetPath("x", "", "/home", adapters.ScopeUser)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/home", ".config", "opencode", "commands", "x.md"), gotUser)
}

func TestComputeContentHash_LFNormalisation(t *testing.T) {
	lfDir := t.TempDir()
	crlfDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(lfDir, "a.md"), []byte("line1\nline2\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(crlfDir, "a.md"), []byte("line1\r\nline2\r\n"), 0o644))

	h1, err := adapters.ComputeContentHash(lfDir)
	require.NoError(t, err)
	h2, err := adapters.ComputeContentHash(crlfDir)
	require.NoError(t, err)
	assert.Equal(t, h1, h2, "hash must be EOL-stable")
}

func TestComputeContentHash_IgnoresMarker(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("body\n"), 0o644))
	withoutMarker, err := adapters.ComputeContentHash(dir)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".skill-version"), []byte("name: x\n"), 0o644))
	withMarker, err := adapters.ComputeContentHash(dir)
	require.NoError(t, err)
	assert.Equal(t, withoutMarker, withMarker)
}

func TestSkillAdapterByID(t *testing.T) {
	assert.Equal(t, "claude-code", adapters.SkillAdapterByID("claude-code").Agent())
	assert.Equal(t, "codex", adapters.SkillAdapterByID("codex").Agent())
	assert.Equal(t, "copilot", adapters.SkillAdapterByID("copilot").Agent())
	assert.Equal(t, "opencode", adapters.SkillAdapterByID("opencode").Agent())
	assert.Nil(t, adapters.SkillAdapterByID("unknown"))
}

func TestMarkerNameConvention(t *testing.T) {
	assert.Equal(t, ".skill-version", adapters.MarkerName("claude-code", "x"))
	assert.Equal(t, ".skill-version", adapters.MarkerName("codex", "x"))
	assert.Equal(t, ".skill-version-x", adapters.MarkerName("copilot", "x"))
	assert.Equal(t, ".skill-version-x", adapters.MarkerName("opencode", "x"))
}
