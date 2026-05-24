package auth_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/internal/portalapi/auth"
)

func TestRoleRank(t *testing.T) {
	assert.Equal(t, 0, auth.RoleRank("anonymous"))
	assert.Equal(t, 1, auth.RoleRank("consumer"))
	assert.Equal(t, 2, auth.RoleRank("author"))
	assert.Equal(t, 3, auth.RoleRank("reviewer"))
	assert.Equal(t, 4, auth.RoleRank("publisher"))
	assert.Equal(t, 5, auth.RoleRank("admin"))
	assert.Equal(t, 0, auth.RoleRank("nonsense"))
}

func TestHasMinRole(t *testing.T) {
	assert.True(t, auth.HasMinRole("admin", "publisher"))
	assert.True(t, auth.HasMinRole("publisher", "publisher"))
	assert.False(t, auth.HasMinRole("author", "publisher"))
	assert.False(t, auth.HasMinRole("anonymous", "consumer"))
}

func TestLoadRoleMap_EmptyPath(t *testing.T) {
	rm, err := auth.LoadRoleMap("")
	require.NoError(t, err)
	assert.Equal(t, "groups", rm.Claim)
	assert.Empty(t, rm.Map)
}

func TestLoadRoleMap_File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rm.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
claim: groups
map:
  fdh-admins: admin
  fdh-reviewers: reviewer
  fdh-authors: author
`), 0o644))
	rm, err := auth.LoadRoleMap(path)
	require.NoError(t, err)
	assert.Equal(t, "groups", rm.Claim)
	assert.Equal(t, "admin", rm.Map["fdh-admins"])
	assert.Equal(t, "reviewer", rm.Map["fdh-reviewers"])
	assert.Equal(t, "author", rm.Map["fdh-authors"])
}

func TestLoadRoleMap_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rm.yaml")
	// Genuinely malformed: unclosed flow mapping.
	require.NoError(t, os.WriteFile(path, []byte("map: {fdh-admins: admin\nclaim: groups"), 0o644))
	_, err := auth.LoadRoleMap(path)
	require.Error(t, err)
}

func TestLoadRoleMap_AlternateClaim(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rm.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
claim: roles
map:
  fdh.admin: admin
`), 0o644))
	rm, err := auth.LoadRoleMap(path)
	require.NoError(t, err)
	assert.Equal(t, "roles", rm.Claim)
}
