package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func execRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd(BuildInfo{Version: "test"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestKindSurface_UnknownVerbErrors(t *testing.T) {
	_, err := execRoot(t, "skill", "frobnicate", "x")
	if err == nil {
		t.Fatalf("expected error for unknown verb, got nil")
	}
}

func TestKindSurface_PluralAliasResolves(t *testing.T) {
	// The plural alias routes to the same parent; an unknown verb under it must
	// still error (proving the alias resolved to the `skill` group).
	_, err := execRoot(t, "skills", "frobnicate", "x")
	if err == nil {
		t.Fatalf("expected plural alias 'skills' to resolve and reject the unknown verb")
	}
}

func TestKindSurface_BackwardCompatTopLevel(t *testing.T) {
	// The top-level install command must remain present and unchanged.
	root := newRootCmd(BuildInfo{Version: "test"})
	found := false
	for _, c := range root.Commands() {
		if c.Name() == "install" {
			found = true
		}
	}
	if !found {
		t.Fatalf("top-level `install` command missing — backward-compat broken")
	}
}

func TestKindNew_ScaffoldAndMaterialize(t *testing.T) {
	tmp := t.TempDir()
	// Make it a detectable project root.
	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(tmp)

	_, err := execRoot(t, "skill", "new", "demo",
		"--agent", "claude-code", "--scope", "project", "--description", "a demo skill")
	if err != nil {
		t.Fatalf("skill new failed: %v", err)
	}

	// Canonical source scaffolded with version 0.1.0.
	src := filepath.Join(tmp, ".fdh", "authoring", "demo", "SKILL.md")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("canonical source not written: %v", err)
	}
	if !bytes.Contains(data, []byte("version: 0.1.0")) {
		t.Fatalf("scaffold missing version 0.1.0:\n%s", data)
	}
	if !bytes.Contains(data, []byte("name: demo")) {
		t.Fatalf("scaffold missing name: demo")
	}

	// Materialized into the claude-code project skills dir, unmanaged.
	mat := filepath.Join(tmp, ".claude", "skills", "demo", "SKILL.md")
	if _, err := os.Stat(mat); err != nil {
		t.Fatalf("not materialized into .claude/skills/demo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".claude", "skills", "demo", ".fdh-managed.yaml")); err == nil {
		t.Fatalf("authored component must be unmanaged (no .fdh-managed.yaml)")
	}
}

func TestKindSync_DriftAndRegenerate(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(tmp)

	if _, err := execRoot(t, "skill", "new", "demo",
		"--agent", "claude-code", "--scope", "project", "--description", "src-desc"); err != nil {
		t.Fatalf("new failed: %v", err)
	}
	mat := filepath.Join(tmp, ".claude", "skills", "demo", "SKILL.md")

	// Introduce drift by editing the materialized copy directly.
	if err := os.WriteFile(mat, []byte("---\nname: demo\nversion: 0.1.0\ndescription: EDITED\n---\ndrifted\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// --check must detect drift and exit non-zero.
	if _, err := execRoot(t, "skill", "sync", "demo", "--agent", "claude-code", "--scope", "project", "--check"); err == nil {
		t.Fatalf("expected --check to report drift (non-zero)")
	}

	// Plain sync regenerates the copy from the canonical source.
	if _, err := execRoot(t, "skill", "sync", "demo", "--agent", "claude-code", "--scope", "project"); err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	data, err := os.ReadFile(mat)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("EDITED")) {
		t.Fatalf("sync did not overwrite the drifted copy:\n%s", data)
	}
	if !bytes.Contains(data, []byte("description: src-desc")) {
		t.Fatalf("materialized copy not restored from canonical source:\n%s", data)
	}
}

func TestKindShare_DryRunPreparesBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// A minimal hub checkout (git repo + hub/registry.yaml with one seed entry).
	hub := t.TempDir()
	reg := "schema_version: 2\nhub_version: \"t\"\ncomponents:\n" +
		"  - name: seed\n    kind: skill\n    description: \"seed\"\n    owner_team: t\n" +
		"    tags: []\n    default: false\n    min_fdh_version: \"0.4.0\"\n" +
		"    agents_supported: [claude-code]\n    path: skills/seed\n"
	if err := os.MkdirAll(filepath.Join(hub, "hub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hub, "hub", "registry.yaml"), []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, a := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"},
		{"add", "-A"}, {"commit", "-q", "-m", "init"}, {"branch", "-M", "main"},
	} {
		if out, err := gitIn(hub, a...); err != nil {
			t.Fatalf("git %v: %v: %s", a, err, out)
		}
	}

	// A project with a canonical source authored under .fdh/authoring/demo.
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := scaffoldBundle(filepath.Join(proj, ".fdh", "authoring", "demo"), "skill", "demo", "a demo skill"); err != nil {
		t.Fatal(err)
	}
	t.Chdir(proj)

	if _, err := execRoot(t, "skill", "share", "demo", "--repo", hub, "--dry-run", "--owner-team", "appsec"); err != nil {
		t.Fatalf("share --dry-run failed: %v", err)
	}

	// Branch created + checked out.
	br, _ := gitIn(hub, "rev-parse", "--abbrev-ref", "HEAD")
	if br != "share/skill/demo" {
		t.Fatalf("expected branch share/skill/demo, got %q", br)
	}
	// Bundle copied into the hub.
	if _, err := os.Stat(filepath.Join(hub, "skills", "demo", "SKILL.md")); err != nil {
		t.Fatalf("bundle not copied into hub: %v", err)
	}
	// Registry entry appended (default:false, correct path, owner team).
	regOut, _ := os.ReadFile(filepath.Join(hub, "hub", "registry.yaml"))
	for _, want := range []string{"name: demo", "default: false", "path: skills/demo", "owner_team: appsec"} {
		if !bytes.Contains(regOut, []byte(want)) {
			t.Fatalf("registry.yaml missing %q:\n%s", want, regOut)
		}
	}
	// Conventional-commit scope authored by the CLI.
	subj, _ := gitIn(hub, "log", "-1", "--format=%s")
	if subj != "feat(demo): add skill" {
		t.Fatalf("expected commit 'feat(demo): add skill', got %q", subj)
	}
}
