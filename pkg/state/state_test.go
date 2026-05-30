package state_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/pkg/state"
)

func TestLoad_MissingReturnsEmpty(t *testing.T) {
	home := t.TempDir()
	s, err := state.Load(home)
	require.NoError(t, err)
	assert.Equal(t, state.SupportedSchemaVersion, s.SchemaVersion)
	assert.Empty(t, s.UserScopeInstalls.Skills)
	assert.Nil(t, s.Projects)
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	home := t.TempDir()
	s := &state.State{
		SchemaVersion: state.SupportedSchemaVersion,
		UserScopeInstalls: state.KindBuckets{
			Skills: []state.InstallEntry{{Name: "a", Version: "0.1.0", Path: "/p"}},
		},
		HubCache: state.HubCache{Commit: "abc", URL: "x"},
	}
	require.NoError(t, state.Save(home, s))
	got, err := state.Load(home)
	require.NoError(t, err)
	require.Len(t, got.UserScopeInstalls.Skills, 1)
	assert.Equal(t, "a", got.UserScopeInstalls.Skills[0].Name)
	assert.Equal(t, "abc", got.HubCache.Commit)
}

func TestUpsertProject(t *testing.T) {
	s := &state.State{SchemaVersion: 1}
	s.UpsertProject("/work/x", state.ProjectEntry{LockHash: "h", ManagedPaths: []string{".claude/skills/x/"}})
	require.Contains(t, s.Projects, "/work/x")
	assert.Equal(t, "h", s.Projects["/work/x"].LockHash)
	assert.False(t, s.Projects["/work/x"].LastInstallAt.IsZero(), "auto-populated timestamp")
}

func TestSetUserScopeInstall_UpsertSemantics(t *testing.T) {
	s := &state.State{SchemaVersion: 1}
	s.SetUserScopeInstall("skill", state.InstallEntry{Name: "a", Version: "0.1.0", Path: "/p", InstalledAt: time.Unix(1, 0)})
	s.SetUserScopeInstall("skill", state.InstallEntry{Name: "a", Version: "0.2.0", Path: "/p", InstalledAt: time.Unix(2, 0)})
	require.Len(t, s.UserScopeInstalls.Skills, 1)
	assert.Equal(t, "0.2.0", s.UserScopeInstalls.Skills[0].Version)
}

func TestRemoveProject(t *testing.T) {
	s := &state.State{SchemaVersion: 1}
	s.UpsertProject("/x", state.ProjectEntry{LockHash: "h"})
	s.RemoveProject("/x")
	assert.NotContains(t, s.Projects, "/x")
}

func TestHashLock_Stable(t *testing.T) {
	a := state.HashLock([]byte("foo"))
	b := state.HashLock([]byte("foo"))
	assert.Equal(t, a, b)
	c := state.HashLock([]byte("bar"))
	assert.NotEqual(t, a, c)
}

func TestSave_CreatesFDHDir(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, state.Save(home, &state.State{SchemaVersion: 1}))
	_, err := os.Stat(filepath.Join(home, ".fdh", "state.json"))
	require.NoError(t, err)
}

func TestLoad_UnsupportedSchemaVersionFails(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".fdh"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".fdh", "state.json"),
		[]byte(`{"schema_version":2,"user_scope_installs":{},"hub_cache":{"last_pulled":"0001-01-01T00:00:00Z"}}`), 0o644))
	_, err := state.Load(home)
	require.Error(t, err)
}
