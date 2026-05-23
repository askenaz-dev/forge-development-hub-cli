package adapters_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/falabella/fdh/pkg/adapters"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDefault_HasFourAgents(t *testing.T) {
	m, err := adapters.LoadDefault()
	require.NoError(t, err)
	ids := m.AgentIDs()
	assert.ElementsMatch(t, []string{"claude-code", "copilot", "codex", "opencode"}, ids)
}

func TestLoadDefault_CopilotBeltAndBraces(t *testing.T) {
	// The Q3 resolution requires every documented Copilot path.
	m, err := adapters.LoadDefault()
	require.NoError(t, err)
	copilot := m.AgentByID("copilot")
	require.NotNil(t, copilot)
	assert.ElementsMatch(t,
		[]string{"~/.copilot/skills/<name>/", "~/.agents/skills/<name>/"},
		copilot.Paths.User,
	)
	assert.ElementsMatch(t,
		[]string{".github/skills/<name>/", ".claude/skills/<name>/", ".agents/skills/<name>/"},
		copilot.Paths.Project,
	)
}

func TestPathSet_FourAgentProjectScope(t *testing.T) {
	m, err := adapters.LoadDefault()
	require.NoError(t, err)

	home := filepath.FromSlash("/home/alice")
	root := filepath.FromSlash("/work/myproj")

	paths, err := m.PathSet(adapters.PathSetOptions{
		SkillName:   "code-review",
		ProjectRoot: root,
		HomeDir:     home,
		Scope:       adapters.ScopeProject,
		AgentIDs:    []string{"claude-code", "copilot", "codex", "opencode"},
	})
	require.NoError(t, err)

	// Must be exactly the three project-scope union paths.
	got := map[string][]string{}
	for _, p := range paths {
		got[p.Path] = p.Agents
	}
	expectedPaths := []string{
		filepath.Join(root, ".claude/skills/code-review"),
		filepath.Join(root, ".agents/skills/code-review"),
		filepath.Join(root, ".github/skills/code-review"),
	}
	assert.Len(t, got, 3, "must produce exactly three deduplicated paths")
	for _, ep := range expectedPaths {
		_, ok := got[ep]
		assert.True(t, ok, "expected path missing: %s", ep)
	}

	// Spot-check: .claude/skills/code-review is satisfied by claude-code,
	// copilot, and opencode.
	claudeDir := filepath.Join(root, ".claude/skills/code-review")
	assert.ElementsMatch(t,
		[]string{"claude-code", "copilot", "opencode"},
		got[claudeDir],
	)
}

func TestPathSet_FourAgentUserScope(t *testing.T) {
	m, err := adapters.LoadDefault()
	require.NoError(t, err)
	home := filepath.FromSlash("/home/bob")

	paths, err := m.PathSet(adapters.PathSetOptions{
		SkillName: "owasp",
		HomeDir:   home,
		Scope:     adapters.ScopeUser,
		AgentIDs:  []string{"claude-code", "copilot", "codex", "opencode"},
	})
	require.NoError(t, err)
	got := map[string]struct{}{}
	for _, p := range paths {
		got[p.Path] = struct{}{}
	}
	expectedPaths := []string{
		filepath.Join(home, ".claude/skills/owasp"),
		filepath.Join(home, ".agents/skills/owasp"),
		filepath.Join(home, ".copilot/skills/owasp"),
	}
	assert.Len(t, got, 3, "user-scope union must produce exactly three paths")
	for _, ep := range expectedPaths {
		_, ok := got[ep]
		assert.True(t, ok, "expected path missing: %s", ep)
	}
}

func TestPathSet_TwoAgentsSharingProjectPath(t *testing.T) {
	// Codex + OpenCode both declare .agents/skills/. OpenCode additionally
	// declares .claude/skills/. Result: two paths, .agents one satisfied
	// by both, .claude one satisfied only by opencode.
	m, err := adapters.LoadDefault()
	require.NoError(t, err)
	root := filepath.FromSlash("/work/proj")
	paths, err := m.PathSet(adapters.PathSetOptions{
		SkillName:   "demo",
		ProjectRoot: root,
		HomeDir:     filepath.FromSlash("/home/x"),
		Scope:       adapters.ScopeProject,
		AgentIDs:    []string{"codex", "opencode"},
	})
	require.NoError(t, err)
	assert.Len(t, paths, 2)
	for _, p := range paths {
		if p.Path == filepath.Join(root, ".agents/skills/demo") {
			assert.ElementsMatch(t, []string{"codex", "opencode"}, p.Agents)
		} else if p.Path == filepath.Join(root, ".claude/skills/demo") {
			assert.Equal(t, []string{"opencode"}, p.Agents)
		} else {
			t.Errorf("unexpected path: %s", p.Path)
		}
	}
}

func TestPathSet_UnknownAgent(t *testing.T) {
	m, err := adapters.LoadDefault()
	require.NoError(t, err)
	_, err = m.PathSet(adapters.PathSetOptions{
		SkillName: "demo",
		HomeDir:   filepath.FromSlash("/home/x"),
		Scope:     adapters.ScopeUser,
		AgentIDs:  []string{"nope"},
	})
	require.Error(t, err)
}

func TestLoadWithOverride_PerAgentReplace(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "adapters.yaml")
	require.NoError(t, os.WriteFile(override, []byte(`
agents:
  - id: claude-code
    display_name: Claude Code (overridden)
    source_doc_url: file:///local
    detect:
      - type: dir-exists
        path: "~/elsewhere"
    paths:
      user:
        - "~/elsewhere/skills/<name>/"
      project:
        - "elsewhere/skills/<name>/"
`), 0o644))

	m, err := adapters.LoadWithOverride(override)
	require.NoError(t, err)
	require.Len(t, m.Agents, 4, "other agents should be preserved")

	claude := m.AgentByID("claude-code")
	require.NotNil(t, claude)
	assert.Equal(t, "Claude Code (overridden)", claude.DisplayName)
	assert.Equal(t, []string{"~/elsewhere/skills/<name>/"}, claude.Paths.User)

	// Other agents untouched.
	codex := m.AgentByID("codex")
	require.NotNil(t, codex)
	assert.Contains(t, codex.Paths.Project, ".agents/skills/<name>/")
}

func TestLoadWithOverride_MissingFileIsOK(t *testing.T) {
	m, err := adapters.LoadWithOverride(filepath.Join(t.TempDir(), "nope.yaml"))
	require.NoError(t, err)
	assert.Len(t, m.Agents, 4)
}

func TestLoadWithOverride_MalformedYAMLFails(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "adapters.yaml")
	require.NoError(t, os.WriteFile(override, []byte("this is :: not yaml ::"), 0o644))
	_, err := adapters.LoadWithOverride(override)
	require.Error(t, err)
}

func TestLoadWithOverride_UnknownFieldRejected(t *testing.T) {
	// KnownFields(true) means a typo'd key fails the load.
	dir := t.TempDir()
	override := filepath.Join(dir, "adapters.yaml")
	require.NoError(t, os.WriteFile(override, []byte(`
agents:
  - id: claude-code
    display_name: Claude Code
    typoed_field: oops
    detect:
      - type: dir-exists
        path: "~/.claude"
    paths:
      user: ["~/.claude/skills/<name>/"]
      project: [".claude/skills/<name>/"]
`), 0o644))
	_, err := adapters.LoadWithOverride(override)
	require.Error(t, err)
}

func TestLoadWithOverride_UnknownProbeTypeRejected(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "adapters.yaml")
	require.NoError(t, os.WriteFile(override, []byte(`
agents:
  - id: claude-code
    display_name: Claude Code
    detect:
      - type: nope-probe
        path: "~/.claude"
    paths:
      user: ["~/.claude/skills/<name>/"]
      project: [".claude/skills/<name>/"]
`), 0o644))
	_, err := adapters.LoadWithOverride(override)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown probe type")
}

func TestExpandPath_HomeTilde(t *testing.T) {
	got, err := adapters.ExpandPath("~/.claude/skills/<name>/", filepath.FromSlash("/home/u"), "", "demo")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/home/u", ".claude/skills/demo"), got)
}

func TestExpandPath_RelativeWithProjectRoot(t *testing.T) {
	got, err := adapters.ExpandPath(".claude/skills/<name>/", "", filepath.FromSlash("/work/proj"), "demo")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/work/proj", ".claude/skills/demo"), got)
}

func TestExpandPath_RelativeWithoutProjectRootErrors(t *testing.T) {
	_, err := adapters.ExpandPath(".claude/skills/<name>/", "", "", "demo")
	require.Error(t, err)
}

func TestEvaluateProbe_DirExistsTrue(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "claude")
	require.NoError(t, os.MkdirAll(target, 0o755))

	ok := adapters.EvaluateProbe(
		adapters.Probe{Type: "dir-exists", Path: "~/claude"},
		adapters.ProbeContext{HomeDir: tmp},
	)
	assert.True(t, ok)
}

func TestEvaluateProbe_DirExistsFalse(t *testing.T) {
	ok := adapters.EvaluateProbe(
		adapters.Probe{Type: "dir-exists", Path: "~/never-exists-12345"},
		adapters.ProbeContext{HomeDir: t.TempDir()},
	)
	assert.False(t, ok)
}

func TestEvaluateProbe_ExecOnPathStubbed(t *testing.T) {
	ctx := adapters.ProbeContext{
		LookPath: func(name string) (string, error) {
			if name == "yes-please" {
				return "/usr/bin/yes-please", nil
			}
			return "", errors.New("not found")
		},
	}
	assert.True(t, adapters.EvaluateProbe(
		adapters.Probe{Type: "exec-on-path", Name: "yes-please"}, ctx))
	assert.False(t, adapters.EvaluateProbe(
		adapters.Probe{Type: "exec-on-path", Name: "nope"}, ctx))
}

func TestCheckWritable_ExistingDir(t *testing.T) {
	tmp := t.TempDir()
	rep := adapters.CheckWritable(tmp)
	assert.Equal(t, adapters.WritableExisting, rep.State)
}

func TestCheckWritable_MissingButCreatable(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "new", "nested")
	rep := adapters.CheckWritable(target)
	assert.Equal(t, adapters.WritableCreatable, rep.State)
}

func TestDetectAll_SmokeTest(t *testing.T) {
	m, err := adapters.LoadDefault()
	require.NoError(t, err)
	// Use an isolated temp HomeDir so we don't pick up the developer's
	// real ~/.claude.
	tmpHome := t.TempDir()
	results := m.DetectAll(adapters.ProbeContext{
		HomeDir:  tmpHome,
		LookPath: func(string) (string, error) { return "", errors.New("none") },
	})
	require.Len(t, results, 4)
	for _, r := range results {
		// With nothing on PATH and no agent dirs in HomeDir, none should detect.
		assert.False(t, r.Detected, "agent %s should not be detected", r.AgentID)
		assert.NotEmpty(t, r.Probes)
	}
}
