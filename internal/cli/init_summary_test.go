package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrintInitInstalls_UserScopeReportsPathAndExplains(t *testing.T) {
	var buf bytes.Buffer
	printInitInstalls(&buf, InitResult{
		SelectedAgents: []string{"claude-code"},
		InstallScope:   "user",
		InstalledSkills: []InstalledSkillResult{
			{Skill: "devsecops", Agent: "claude-code", TargetPath: "/home/me/.claude/skills/devsecops"},
		},
	})
	out := buf.String()
	assert.Contains(t, out, "agents:  claude-code")
	assert.Contains(t, out, "Installed 1 component(s) at user scope")
	assert.Contains(t, out, "devsecops")
	assert.Contains(t, out, "/home/me/.claude/skills/devsecops")
	// The user-scope surprise must be spelled out, and point at --local.
	assert.Contains(t, out, "user scope")
	assert.Contains(t, out, "--local")
}

func TestPrintInitInstalls_GroupsAgentsAndFlagsSkipped(t *testing.T) {
	var buf bytes.Buffer
	printInitInstalls(&buf, InitResult{
		InstallScope: "project",
		InstalledSkills: []InstalledSkillResult{
			{Skill: "spec", Agent: "claude-code", TargetPath: "/p/.claude/skills/spec", Skipped: true},
			{Skill: "spec", Agent: "codex", TargetPath: "/p/.agents/skills/spec", Skipped: true},
		},
	})
	out := buf.String()
	assert.Contains(t, out, "at project scope")
	assert.Contains(t, out, "already up to date")
	// Both target paths listed once under the single component.
	assert.Contains(t, out, "/p/.claude/skills/spec")
	assert.Contains(t, out, "/p/.agents/skills/spec")
	// Project scope does NOT print the user-scope explanation.
	assert.NotContains(t, out, "git init")
}

func TestPrintInitInstalls_NothingInstalledIsQuiet(t *testing.T) {
	var buf bytes.Buffer
	printInitInstalls(&buf, InitResult{})
	assert.Empty(t, buf.String())
}

func TestApplyLocalScope_PinsProjectRootToCwd(t *testing.T) {
	dir := t.TempDir() // a plain dir, no .git, no .fdh
	t.Chdir(dir)

	rc := &runContext{ProjectRoot: ""} // as if no project was detected
	require.NoError(t, applyLocalScope(rc))

	gotResolved, _ := filepath.EvalSymlinks(rc.ProjectRoot)
	dirResolved, _ := filepath.EvalSymlinks(dir)
	assert.Equal(t, dirResolved, gotResolved, "--local must root the project at cwd")
}

func TestApplyLocalScope_OverridesDetectedRoot(t *testing.T) {
	// Even when a parent git root would be detected, --local pins to cwd:
	// the flag means "install into THIS directory".
	dir := t.TempDir()
	t.Chdir(dir)
	rc := &runContext{ProjectRoot: filepath.Join(dir, "..", "some-parent-repo")}
	require.NoError(t, applyLocalScope(rc))
	gotResolved, _ := filepath.EvalSymlinks(rc.ProjectRoot)
	dirResolved, _ := filepath.EvalSymlinks(dir)
	assert.Equal(t, dirResolved, gotResolved)
}

func TestDetectProjectRoot_FdhManifestAnchor(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".fdh"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".fdh", "manifest.yaml"), []byte("schema_version: 1\n"), 0o644))

	t.Chdir(dir)
	got := detectProjectRoot()
	// macOS tempdirs are symlinked (/var → /private/var); compare resolved.
	gotResolved, _ := filepath.EvalSymlinks(got)
	dirResolved, _ := filepath.EvalSymlinks(dir)
	assert.Equal(t, dirResolved, gotResolved)
}

func TestDetectProjectRoot_BareFdhDirIsNotAnAnchor(t *testing.T) {
	// A bare .fdh/ directory (e.g. ~/.fdh/bin from the standalone installer)
	// must NOT be treated as a project root — only .fdh/manifest.yaml is.
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".fdh", "bin"), 0o755))

	t.Chdir(dir)
	got := detectProjectRoot()
	dirResolved, _ := filepath.EvalSymlinks(dir)
	gotResolved, _ := filepath.EvalSymlinks(got)
	assert.NotEqual(t, dirResolved, gotResolved, "bare .fdh/ must not anchor a project root")
}
