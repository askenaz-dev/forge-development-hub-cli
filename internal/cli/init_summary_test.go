package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/pkg/adapters"
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
	// The user-scope case must be spelled out, and point at --global.
	assert.Contains(t, out, "user/home scope")
	assert.Contains(t, out, "--global")
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

func TestResolveScope_DefaultRootsAtCwdWhenNoAnchor(t *testing.T) {
	dir := t.TempDir() // a plain dir, no .git, no .fdh
	t.Chdir(dir)

	rc := &runContext{ProjectRoot: ""} // as if no project was detected
	got, err := resolveScope("", rc)   // local-by-default
	require.NoError(t, err)
	assert.Equal(t, adapters.ScopeProject, got)

	gotResolved, _ := filepath.EvalSymlinks(rc.ProjectRoot)
	dirResolved, _ := filepath.EvalSymlinks(dir)
	assert.Equal(t, dirResolved, gotResolved, "default scope must root the project at cwd")
}

func TestResolveScope_DefaultKeepsDetectedRoot(t *testing.T) {
	// When an anchor was already detected, the default must NOT override it
	// with the cwd — it installs at the detected project root.
	detected := filepath.Join(t.TempDir(), "repo")
	rc := &runContext{ProjectRoot: detected}
	got, err := resolveScope("auto", rc)
	require.NoError(t, err)
	assert.Equal(t, adapters.ScopeProject, got)
	assert.Equal(t, detected, rc.ProjectRoot)
}

func TestResolveScope_UserIsHomeScope(t *testing.T) {
	rc := &runContext{ProjectRoot: ""}
	got, err := resolveScope("user", rc)
	require.NoError(t, err)
	assert.Equal(t, adapters.ScopeUser, got)
	assert.Empty(t, rc.ProjectRoot, "user scope must not pin a project root")
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
