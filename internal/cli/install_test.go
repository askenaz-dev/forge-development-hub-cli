package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/falabella/fdh/internal/testutil"
	"github.com/falabella/fdh/pkg/adapters"
	"github.com/falabella/fdh/pkg/bundle"
	"github.com/falabella/fdh/pkg/portability"
	"github.com/falabella/fdh/pkg/provenance"
	"github.com/falabella/fdh/pkg/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This package-level test exercises the install pipeline end-to-end without
// going through cobra. The CLI command code is thin glue over the same
// pipeline (resolve → lint → path-set → fan-out write → sidecar), so testing
// the pipeline at this level gives high coverage without subprocess overhead.

type pipelineFixture struct {
	t           *testing.T
	registry    *registry.GitRegistry
	manifest    *adapters.Manifest
	homeDir     string
	projectRoot string
	registryDir string
}

func newPipelineFixture(t *testing.T) *pipelineFixture {
	t.Helper()
	// Build a fixture registry.
	regDir := t.TempDir()
	testutil.BuildRegistry(t, regDir, []testutil.SkillSpec{
		{
			Namespace:   "code-review",
			Name:        "standard",
			Version:     "1.0.0",
			Description: "Code review checklist.",
			OwnerTeam:   "dx",
			Tags:        []string{"review"},
			Files: map[string]string{
				"SKILL.md":            testutil.FixtureSKILLMD("standard", "Code review checklist."),
				"references/notes.md": "Notes for reviewers.",
			},
		},
		{
			Namespace:   "security",
			Name:        "claude-only-skill",
			Version:     "1.0.0",
			Description: "Non-portable Claude-only skill.",
			OwnerTeam:   "appsec",
			Files: map[string]string{
				"SKILL.md": `---
name: claude-only-skill
description: Non-portable Claude-only skill.
portable: false
compatibility:
  - claude-code
---
Use $ARGUMENTS to investigate.
`,
			},
		},
	})

	// Use a custom adapter manifest so we don't depend on the developer's
	// real home directory layout. We point all four agents at directories
	// under the test's temp home.
	home := t.TempDir()
	root := t.TempDir()

	// Pre-create per-agent dirs so probes "detect" each agent.
	for _, sub := range []string{".claude", ".agents", ".copilot", ".config/opencode"} {
		require.NoError(t, os.MkdirAll(filepath.Join(home, sub), 0o755))
	}

	mani, err := adapters.LoadDefault()
	require.NoError(t, err)

	reg := &registry.GitRegistry{
		LocalPath: regDir,
		SkipFetch: true,
	}

	return &pipelineFixture{
		t:           t,
		registry:    reg,
		manifest:    mani,
		homeDir:     home,
		projectRoot: root,
		registryDir: regDir,
	}
}

// installInPipeline replicates what internal/cli.runInstall does without the
// cobra layer. It is the "real" pipeline under test.
func (f *pipelineFixture) installInPipeline(skillRef string, scope adapters.Scope, agentIDs []string) (*pipelineResult, error) {
	t := f.t
	t.Helper()

	parts := strings.SplitN(skillRef, "/", 2)
	namespace := parts[0]
	rest := parts[1]
	version := ""
	if at := strings.Index(rest, "@"); at >= 0 {
		version = rest[at+1:]
		rest = rest[:at]
	}
	name := rest

	ctx := context.Background()
	man, err := f.registry.Manifest(ctx, namespace, name)
	if err != nil {
		return nil, err
	}
	if version == "" {
		version = man.Latest
	}

	bp, err := f.registry.FetchBundle(ctx, namespace, name, version)
	if err != nil {
		return nil, err
	}
	defer bp.Cleanup()

	b, err := bundle.Load(bp.Path)
	if err != nil {
		return nil, err
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	known := f.manifest.AgentIDs()
	lint := portability.Lint(b, portability.LintOptions{KnownAgentIDs: known})
	if portability.HasErrors(lint) {
		return nil, errors.New(portability.Format(lint))
	}

	if len(agentIDs) == 0 {
		// Use the manifest's defined agent IDs as the "detected" set.
		agentIDs = known
	}
	// Apply compatibility filter.
	if !b.SkillMD.IsPortable() {
		allow := map[string]struct{}{}
		for _, c := range b.SkillMD.Compatibility {
			allow[c] = struct{}{}
		}
		var filtered []string
		for _, id := range agentIDs {
			if _, ok := allow[id]; ok {
				filtered = append(filtered, id)
			}
		}
		agentIDs = filtered
	}

	paths, err := f.manifest.PathSet(adapters.PathSetOptions{
		SkillName:   b.SkillMD.Name,
		ProjectRoot: f.projectRoot,
		HomeDir:     f.homeDir,
		Scope:       scope,
		AgentIDs:    agentIDs,
	})
	if err != nil {
		return nil, err
	}

	breadcrumb := provenance.MakeBreadcrumbRef(f.registry.Source(), namespace, name, version)

	for _, p := range paths {
		require.NoError(t, writeBundleToPathTest(bp.Path, p.Path, breadcrumb))
		meta := provenance.SkillMeta{
			Registry:         f.registry.Source(),
			Namespace:        namespace,
			Name:             name,
			Version:          version,
			ContentHash:      bp.Hash,
			InstalledBy:      "test@host",
			TargetAgents:     append([]string(nil), p.Agents...),
			Scope:            string(scope),
			Path:             p.Path,
			InstallerVersion: "test",
		}
		require.NoError(t, provenance.WriteSidecar(p.Path, meta))
	}

	return &pipelineResult{
		Namespace: namespace, Name: name, Version: version,
		Hash: bp.Hash, Paths: paths,
	}, nil
}

type pipelineResult struct {
	Namespace string
	Name      string
	Version   string
	Hash      string
	Paths     []adapters.ResolvedPath
}

func writeBundleToPathTest(src, dst, breadcrumb string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(filepath.Base(p), ".") {
			return nil
		}
		dest := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		if rel == "SKILL.md" {
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out := provenance.InjectBreadcrumb(raw, breadcrumb)
			return os.WriteFile(dest, out, 0o644)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
}

// --- tests ---

func TestPipeline_PortableSkill_FourAgentProjectScope(t *testing.T) {
	f := newPipelineFixture(t)

	res, err := f.installInPipeline("code-review/standard", adapters.ScopeProject, nil)
	require.NoError(t, err)

	// Per the agent-adapter-map spec: project-scope four-agent install
	// writes to exactly three paths.
	assert.Len(t, res.Paths, 3, "expected exactly three project-scope paths for 4-agent install")

	// Each path has a SKILL.md plus the .skill-meta.yaml sidecar.
	for _, p := range res.Paths {
		skillFile := filepath.Join(p.Path, "SKILL.md")
		info, err := os.Stat(skillFile)
		require.NoError(t, err, "SKILL.md missing in %s", p.Path)
		require.True(t, info.Size() > 0)

		meta, err := provenance.ReadSidecar(p.Path)
		require.NoError(t, err)
		assert.Equal(t, "standard", meta.Name)
		assert.Equal(t, "code-review", meta.Namespace)
		assert.Equal(t, "1.0.0", meta.Version)
		assert.Equal(t, res.Hash, meta.ContentHash)
		assert.ElementsMatch(t, p.Agents, meta.TargetAgents)
	}
}

func TestPipeline_HashEqualsRegistryHash(t *testing.T) {
	f := newPipelineFixture(t)
	res, err := f.installInPipeline("code-review/standard", adapters.ScopeUser, nil)
	require.NoError(t, err)

	// The sidecar's content_hash must equal what BuildRegistry recorded
	// (the canonical hash of the source bundle, not the post-injection file).
	for _, p := range res.Paths {
		meta, err := provenance.ReadSidecar(p.Path)
		require.NoError(t, err)
		assert.Equal(t, res.Hash, meta.ContentHash)
	}
}

func TestPipeline_BreadcrumbInjectedAndStrippable(t *testing.T) {
	f := newPipelineFixture(t)
	res, err := f.installInPipeline("code-review/standard", adapters.ScopeUser, nil)
	require.NoError(t, err)

	// Pick any installed path and verify the breadcrumb is present.
	require.NotEmpty(t, res.Paths)
	installed, err := os.ReadFile(filepath.Join(res.Paths[0].Path, "SKILL.md"))
	require.NoError(t, err)
	assert.Contains(t, string(installed), "installed_from: ")

	// Stripping the breadcrumb must yield byte-identical content to the
	// source bundle on disk.
	source, err := os.ReadFile(filepath.Join(f.registryDir, "skills", "code-review", "standard", "versions", "1.0.0", "bundle", "SKILL.md"))
	require.NoError(t, err)
	stripped := provenance.StripBreadcrumb(installed)
	assert.Equal(t, source, stripped, "stripping breadcrumb must yield the original byte-for-byte")
}

func TestPipeline_NonPortableSkill_RefusesNonCompatibleAgent(t *testing.T) {
	f := newPipelineFixture(t)
	// Target copilot for a Claude-only skill — should produce zero paths
	// because the compatibility filter drops copilot.
	res, err := f.installInPipeline("security/claude-only-skill", adapters.ScopeUser, []string{"copilot"})
	require.NoError(t, err)
	assert.Empty(t, res.Paths, "no copilot path should be written for a claude-only skill")
}

func TestPipeline_NonPortableSkill_InstallsToCompatibleAgent(t *testing.T) {
	f := newPipelineFixture(t)
	res, err := f.installInPipeline("security/claude-only-skill", adapters.ScopeUser, []string{"claude-code"})
	require.NoError(t, err)
	require.NotEmpty(t, res.Paths)
	// Claude-only skills install to ~/.claude/skills only.
	for _, p := range res.Paths {
		assert.Contains(t, p.Agents, "claude-code")
	}
}

func TestPipeline_HashMismatchAborts(t *testing.T) {
	f := newPipelineFixture(t)
	// Corrupt the recorded sha256 of standard@1.0.0.
	sumFile := filepath.Join(f.registryDir, "skills", "code-review", "standard", "versions", "1.0.0", "bundle.sha256")
	require.NoError(t, os.WriteFile(sumFile,
		[]byte("0000000000000000000000000000000000000000000000000000000000000000  bundle.tar.gz\n"), 0o644))

	_, err := f.installInPipeline("code-review/standard", adapters.ScopeUser, nil)
	require.Error(t, err)
}

// TestPipeline_NestedFilesPreserved exercises bundles with subdirectories.
func TestPipeline_NestedFilesPreserved(t *testing.T) {
	f := newPipelineFixture(t)
	res, err := f.installInPipeline("code-review/standard", adapters.ScopeUser, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Paths)
	for _, p := range res.Paths {
		_, err := os.Stat(filepath.Join(p.Path, "references", "notes.md"))
		require.NoError(t, err, "references/notes.md missing in %s", p.Path)
	}
}

func TestPipeline_E2EJSONStructure(t *testing.T) {
	// Sanity that we can serialise a result via the same JSON encoder the
	// CLI uses, and the bytes round-trip cleanly.
	f := newPipelineFixture(t)
	res, err := f.installInPipeline("code-review/standard", adapters.ScopeUser, nil)
	require.NoError(t, err)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	require.NoError(t, enc.Encode(res))

	var roundTrip pipelineResult
	require.NoError(t, json.Unmarshal(buf.Bytes(), &roundTrip))
	assert.Equal(t, res.Version, roundTrip.Version)
	assert.Equal(t, res.Hash, roundTrip.Hash)
}

// TestPipeline_LineEndingsPreserved verifies that re-installing a bundle
// whose SKILL.md uses LF line endings does not silently convert them to
// CRLF on Windows (the install pipeline writes raw bytes).
func TestPipeline_LineEndingsPreserved(t *testing.T) {
	// Build a fixture explicitly with LF endings.
	regDir := t.TempDir()
	testutil.BuildRegistry(t, regDir, []testutil.SkillSpec{
		{
			Namespace:   "demo",
			Name:        "lf-fixture",
			Version:     "1.0.0",
			Description: "LF endings preserved.",
			OwnerTeam:   "dx",
			Files: map[string]string{
				"SKILL.md": "---\nname: lf-fixture\ndescription: LF endings preserved.\n---\nbody line one\nbody line two\n",
			},
		},
	})
	mani, _ := adapters.LoadDefault()
	home := t.TempDir()
	root := t.TempDir()
	for _, sub := range []string{".claude", ".agents", ".copilot"} {
		require.NoError(t, os.MkdirAll(filepath.Join(home, sub), 0o755))
	}
	reg := &registry.GitRegistry{LocalPath: regDir, SkipFetch: true}

	bp, err := reg.FetchBundle(context.Background(), "demo", "lf-fixture", "1.0.0")
	require.NoError(t, err)
	defer bp.Cleanup()

	b, err := bundle.Load(bp.Path)
	require.NoError(t, err)
	paths, err := mani.PathSet(adapters.PathSetOptions{
		SkillName: b.SkillMD.Name, HomeDir: home, ProjectRoot: root,
		Scope: adapters.ScopeUser, AgentIDs: []string{"claude-code"},
	})
	require.NoError(t, err)
	require.NoError(t, writeBundleToPathTest(bp.Path, paths[0].Path, "ref/x@1"))

	out, err := os.ReadFile(filepath.Join(paths[0].Path, "SKILL.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(out), "\r\n", "LF endings must survive the install round-trip")
}

// --- CLI subprocess smoke (sanity that the binary runs the commands without
//     crashing). We do not assert exit codes here because cobra's exit
//     handling differs from our wrapped error path during test execution.

func TestBinary_Help(t *testing.T) {
	binary := buildBinary(t)
	out, err := runBinary(binary, "--help")
	require.NoError(t, err)
	assert.Contains(t, out, "Usage:")
	assert.Contains(t, out, "install")
	assert.Contains(t, out, "doctor")
}

func TestBinary_Version(t *testing.T) {
	binary := buildBinary(t)
	out, err := runBinary(binary, "--version")
	require.NoError(t, err)
	assert.Contains(t, strings.ToLower(out), "fdh")
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fdh")
	if isWindows() {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/fdh")
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	return bin
}

func runBinary(bin string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod above cwd")
		}
		dir = parent
	}
}

func isWindows() bool {
	return runtime.GOOS == "windows"
}

// io.Writer is used implicitly via *bytes.Buffer above; keep io imported.
var _ = io.Discard
