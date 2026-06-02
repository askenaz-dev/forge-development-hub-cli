package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkScopeCmd builds a bare command carrying the --global / --scope flags so
// scopeFromFlags can be exercised in isolation.
func mkScopeCmd(t *testing.T, global bool, scope string) *cobra.Command {
	t.Helper()
	c := &cobra.Command{}
	c.Flags().Bool("global", false, "")
	c.Flags().String("scope", "", "")
	if global {
		require.NoError(t, c.Flags().Set("global", "true"))
	}
	if scope != "" {
		require.NoError(t, c.Flags().Set("scope", scope))
	}
	return c
}

func TestScopeFromFlags_GlobalWins(t *testing.T) {
	s, err := scopeFromFlags(mkScopeCmd(t, true, "auto"))
	require.NoError(t, err)
	assert.Equal(t, "user", s, "--global selects user/home scope")
}

func TestScopeFromFlags_NoFlagsIsLocalDefault(t *testing.T) {
	s, err := scopeFromFlags(mkScopeCmd(t, false, ""))
	require.NoError(t, err)
	// Empty string flows into resolveScope which defaults to project@CWD.
	assert.Equal(t, "", s)
}

func TestScopeFromFlags_GlobalConflictsWithScopeProject(t *testing.T) {
	_, err := scopeFromFlags(mkScopeCmd(t, true, "project"))
	require.Error(t, err)
	assert.Equal(t, ExitInvalidUsage, ExitCode(err))
}

func TestScopeFromFlags_GlobalWithScopeUserIsAllowed(t *testing.T) {
	s, err := scopeFromFlags(mkScopeCmd(t, true, "user"))
	require.NoError(t, err)
	assert.Equal(t, "user", s)
}

func TestScopeFromFlags_ExplicitScopePassesThrough(t *testing.T) {
	s, err := scopeFromFlags(mkScopeCmd(t, false, "project"))
	require.NoError(t, err)
	assert.Equal(t, "project", s)
}

// The --local flag is gone (local is the default); --global is its replacement.
func TestInstallCmd_HasGlobalNotLocal(t *testing.T) {
	c := newInstallCmd(BuildInfo{})
	assert.Nil(t, c.Flags().Lookup("local"), "--local must be removed")
	assert.NotNil(t, c.Flags().Lookup("global"), "--global must exist")
}

func TestInitCmd_HasGlobalNotLocal(t *testing.T) {
	c := newInitCmd(BuildInfo{})
	assert.Nil(t, c.Flags().Lookup("local"), "--local must be removed")
	assert.NotNil(t, c.Flags().Lookup("global"), "--global must exist")
}
