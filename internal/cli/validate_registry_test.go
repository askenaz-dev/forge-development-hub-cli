package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/forge/fdh/pkg/hubregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildRepoRoot stages a repo root with skills/registry.yaml + the
// referenced skill directories. yaml is written verbatim;
// skillsOnDisk is the list of `skills/<name>/` directories to
// materialise.
func buildRepoRoot(t *testing.T, yaml string, skillsOnDisk []string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "skills"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "skills", "registry.yaml"), []byte(yaml), 0o644))
	for _, s := range skillsOnDisk {
		dir := filepath.Join(root, "skills", s)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# "+s+"\n"), 0o644))
	}
	return root
}

// runRoot executes the root command with the given args. Returns
// stdout, stderr, and the error (whose ExitCode is the CLI exit
// code via ExitCode()).
func runRoot(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCmd(BuildInfo{Version: "0.0.0-test"})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func TestValidateRegistry_GoldenRegistryOK(t *testing.T) {
	yaml := `schema_version: 1
skills:
  - name: design-system
    path: skills/design-system
    agents_supported: [claude-code]
  - name: code-review
    path: skills/code-review
    agents_supported: [claude-code, copilot]
`
	root := buildRepoRoot(t, yaml, []string{"design-system", "code-review"})
	stdout, _, err := runRoot(t, "validate-registry", root)
	require.NoError(t, err)
	assert.Contains(t, stdout, "ok:")
}

func TestValidateRegistry_JSONOutput(t *testing.T) {
	yaml := `schema_version: 1
skills:
  - name: x
    path: skills/x
    agents_supported: [claude-code]
`
	root := buildRepoRoot(t, yaml, []string{"x"})
	stdout, _, err := runRoot(t, "--json", "validate-registry", root)
	require.NoError(t, err)

	var got ValidateRegistryResult
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.True(t, got.OK)
	assert.Empty(t, got.Errors)
}

func TestValidateRegistry_DuplicateName(t *testing.T) {
	yaml := `schema_version: 1
skills:
  - name: x
    path: skills/x
    agents_supported: [claude-code]
  - name: x
    path: skills/y
    agents_supported: [codex]
`
	root := buildRepoRoot(t, yaml, []string{"x", "y"})
	stdout, _, err := runRoot(t, "--json", "validate-registry", root)
	require.Error(t, err)
	assert.Equal(t, ExitValidation, ExitCode(err))

	var got ValidateRegistryResult
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.False(t, got.OK)
	requireRule(t, got.Errors, "unique-name")
}

func TestValidateRegistry_OrphanDirectory(t *testing.T) {
	yaml := `schema_version: 1
skills:
  - name: x
    path: skills/x
    agents_supported: [claude-code]
`
	root := buildRepoRoot(t, yaml, []string{"x", "y"})
	stdout, _, err := runRoot(t, "--json", "validate-registry", root)
	require.Error(t, err)

	var got ValidateRegistryResult
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	requireRule(t, got.Errors, "no-orphans")
}

func TestValidateRegistry_MissingPath(t *testing.T) {
	yaml := `schema_version: 1
skills:
  - name: ghost
    path: skills/ghost
    agents_supported: [claude-code]
`
	root := buildRepoRoot(t, yaml, nil)
	stdout, _, err := runRoot(t, "--json", "validate-registry", root)
	require.Error(t, err)

	var got ValidateRegistryResult
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	requireRule(t, got.Errors, "path-exists")
}

func TestValidateRegistry_EmptyAgentsSupported(t *testing.T) {
	yaml := `schema_version: 1
skills:
  - name: x
    path: skills/x
    agents_supported: []
`
	root := buildRepoRoot(t, yaml, []string{"x"})
	stdout, _, err := runRoot(t, "--json", "validate-registry", root)
	require.Error(t, err)

	var got ValidateRegistryResult
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	requireRule(t, got.Errors, "agents-supported-nonempty")
}

func TestValidateRegistry_UnsupportedSchemaVersion(t *testing.T) {
	yaml := `schema_version: 99
skills: []
`
	root := buildRepoRoot(t, yaml, nil)
	stdout, _, err := runRoot(t, "--json", "validate-registry", root)
	require.Error(t, err)

	var got ValidateRegistryResult
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	requireRule(t, got.Errors, "schema-version")
}

func TestValidateRegistry_BadYAML(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "skills"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "skills", "registry.yaml"), []byte("not: [valid"), 0o644))

	stdout, _, err := runRoot(t, "--json", "validate-registry", root)
	require.Error(t, err)
	assert.Equal(t, ExitValidation, ExitCode(err))

	var got ValidateRegistryResult
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	requireRule(t, got.Errors, "yaml-syntax")
}

func TestValidateRegistry_RepoRootMissing(t *testing.T) {
	_, _, err := runRoot(t, "validate-registry", filepath.Join(t.TempDir(), "nope"))
	require.Error(t, err)
	assert.Equal(t, ExitInvalidUsage, ExitCode(err))
}

func requireRule(t *testing.T, errs []hubregistry.ValidationError, rule string) {
	t.Helper()
	for _, e := range errs {
		if e.Rule == rule {
			return
		}
	}
	t.Fatalf("expected rule %q in errors, got %+v", rule, errs)
}
