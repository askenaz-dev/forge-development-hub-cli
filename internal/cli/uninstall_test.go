package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/pkg/adapters"
	"github.com/forge/fdh/pkg/managed"
)

func TestUninstall_FindCandidates_MatchesByName(t *testing.T) {
	hub := buildWizardHub(t)
	rc := buildWizardRC(t)
	installFixtureSkill(t, hub, rc, "design-system")

	// Confirm marker exists pre-uninstall.
	dir := filepath.Join(rc.ProjectRoot, ".claude", "skills", "design-system")
	_, err := os.Stat(filepath.Join(dir, managed.Filename))
	require.NoError(t, err)

	cands, err := findUninstallCandidates(rc, adapters.ScopeProject, "design-system")
	require.NoError(t, err)
	require.Len(t, cands, 1)
	assert.Equal(t, "claude-code", cands[0].agent)
	assert.Equal(t, managed.KindSkill, cands[0].kind)
	assert.Equal(t, dir, cands[0].removePath)
}

func TestUninstall_NoMatch_ReturnsEmpty(t *testing.T) {
	_ = buildWizardHub(t)
	rc := buildWizardRC(t)

	cands, err := findUninstallCandidates(rc, adapters.ScopeProject, "missing")
	require.NoError(t, err)
	assert.Empty(t, cands)
}
