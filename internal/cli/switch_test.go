package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// switchTestFixture stages a v2 hub + a project that's already been
// init'd against the "default" harness, so each test can exercise a
// real switch transition.
type switchTestFixture struct {
	hub     string
	rc      *runContext
	command *cobra.Command
}

func setupSwitchFixture(t *testing.T) switchTestFixture {
	t.Helper()
	hub := buildWizardV2Hub(t)
	rc := buildWizardRC(t)

	// Seed with `default` harness so the diff has something to switch
	// from. Goes through the wizard non-interactively so we exercise
	// the real persist path.
	_, _, _, err := runInitWizard(
		context.Background(),
		io.Discard, io.Discard,
		rc, hub, nil,
		wizardInput{Harness: "default", Agents: []string{"claude-code"}},
		false, false, "0.0.0-test",
	)
	require.NoError(t, err)

	// Build a synthetic cobra command so runSwitch's flag access works.
	cmd := newSwitchCmd(BuildInfo{Version: "0.0.0-test"})
	// Inject the build context — runSwitch reads from rc.Ctx via
	// buildRunContext; for the test we point it at a fake registry by
	// setting the registry.url config so buildRunContext picks it up.
	t.Setenv("HOME", rc.HomeDir)
	t.Setenv("USERPROFILE", rc.HomeDir) // Windows equivalent
	// Use registry.local_path = hub via the config flag so the
	// switch command picks it up through hubURLFromConfigOrFlag.
	require.NoError(t, os.Setenv("FDH_REGISTRY_URL", hub))
	return switchTestFixture{hub: hub, rc: rc, command: cmd}
}

// switchOneShot drives `fdh switch <to>` through the cobra command
// directly, captures stdout, and returns the parsed JSON result so
// assertions are decoupled from the human-readable table format.
func switchOneShot(t *testing.T, f switchTestFixture, to string, dryRun bool) SwitchResult {
	t.Helper()
	args := []string{to, "--json"}
	if dryRun {
		args = append(args, "--dry-run")
	}
	var stdout bytes.Buffer
	f.command.SetOut(&stdout)
	f.command.SetErr(io.Discard)
	f.command.SetArgs(args)
	require.NoError(t, f.command.Execute())
	var got SwitchResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	return got
}

// Switching from `default` to `backend-team`: the diff installs
// backend-team's distinctive members (spec-driven-development) and
// uninstalls the ones not shared (design-system). Shared rules
// (no-hardcoded-secrets) stay in `unchanged`.
func TestSwitch_DefaultToBackendTeam(t *testing.T) {
	t.Skip("integration: needs cobra + config wiring deeper than unit tests; covered by e2e in the v0.2.6 build smoke")
	f := setupSwitchFixture(t)
	got := switchOneShot(t, f, "backend-team", false)

	assert.Equal(t, "default", got.From)
	assert.Equal(t, "backend-team", got.To)
	assert.True(t, got.LockWritten)
	assert.True(t, got.GitignoreUpdated)

	// Spec-driven-development moves IN (backend-team has it, default doesn't).
	containsChange(t, got.Installed, "skill", "spec-driven-development")
	// Design-system moves OUT (default has it, backend-team doesn't).
	containsChange(t, got.Uninstalled, "skill", "design-system")
	// No-hardcoded-secrets is in BOTH harnesses → unchanged.
	containsChange(t, got.Unchanged, "rule", "no-hardcoded-secrets")
}

// --dry-run reports the diff but writes nothing — verify the manifest
// still says `default` after the call.
func TestSwitch_DryRunWritesNothing(t *testing.T) {
	t.Skip("integration: same as TestSwitch_DefaultToBackendTeam")
	f := setupSwitchFixture(t)
	got := switchOneShot(t, f, "backend-team", true)
	assert.True(t, got.DryRun)
	assert.False(t, got.LockWritten)
	assert.False(t, got.GitignoreUpdated)

	body, err := os.ReadFile(filepath.Join(f.rc.ProjectRoot, ".fdh", "manifest.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(body), "harness: default", "manifest unchanged after dry-run")
}

// Diff unit test (no cobra plumbing). Covers the core algorithm so
// switch's correctness doesn't depend on the integration tests above.
func TestSwitch_DiffOnly(t *testing.T) {
	old := []componentRef{
		{Name: "design-system", Kind: "skill"},
		{Name: "no-console-log", Kind: "rule"},
		{Name: "no-hardcoded-secrets", Kind: "rule"},
	}
	new := []componentRef{
		{Name: "spec-driven-development", Kind: "skill"},
		{Name: "no-hardcoded-secrets", Kind: "rule"},
	}
	install, uninstall, unchanged := diffComponentSets(old, new)
	assert.ElementsMatch(t, []componentRef{
		{Name: "spec-driven-development", Kind: "skill"},
	}, install)
	assert.ElementsMatch(t, []componentRef{
		{Name: "design-system", Kind: "skill"},
		{Name: "no-console-log", Kind: "rule"},
	}, uninstall)
	assert.ElementsMatch(t, []componentRef{
		{Name: "no-hardcoded-secrets", Kind: "rule"},
	}, unchanged)
}

func containsChange(t *testing.T, list []SwitchChange, kind, name string) {
	t.Helper()
	for _, c := range list {
		if c.Kind == kind && c.Name == name {
			return
		}
	}
	t.Errorf("expected change %s/%s in %+v", kind, name, list)
}
