package managed_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/pkg/managed"
)

func TestFilenameFor(t *testing.T) {
	assert.Equal(t, ".fdh-managed.yaml", managed.FilenameFor("design-system", false))
	assert.Equal(t, "review.prompt.md.fdh-managed.yaml", managed.FilenameFor("review.prompt.md", true))
}

func TestIsManagedFilename(t *testing.T) {
	assert.True(t, managed.IsManagedFilename(".fdh-managed.yaml"))
	assert.True(t, managed.IsManagedFilename("review.prompt.md.fdh-managed.yaml"))
	assert.False(t, managed.IsManagedFilename(".skill-version"))
	assert.False(t, managed.IsManagedFilename("README.md"))
}

func TestIsLegacyFilename(t *testing.T) {
	assert.True(t, managed.IsLegacyFilename(".skill-version"))
	assert.True(t, managed.IsLegacyFilename(".skill-version-design-system"))
	assert.False(t, managed.IsLegacyFilename(".fdh-managed.yaml"))
	assert.False(t, managed.IsLegacyFilename(".skill-version-"))
}

func TestWriteRead_RoundTripDir(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	m := managed.Marker{
		Name:           "design-system",
		Kind:           "skill",
		Version:        "0.4.0",
		HubCommit:      "abc123",
		InstalledAt:    now,
		InstalledByFDH: "0.7.0",
		SourcePath:     "skills/design-system",
		ContentHash:    "sha256:deadbeef",
		Agent:          "claude-code",
	}
	path, err := managed.Write(dir, "", m, false)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, ".fdh-managed.yaml"), path)

	got, err := managed.Read(path)
	require.NoError(t, err)
	assert.Equal(t, m.Name, got.Name)
	assert.Equal(t, m.Kind, got.Kind)
	assert.Equal(t, m.Version, got.Version)
	assert.Equal(t, m.HubCommit, got.HubCommit)
	assert.Equal(t, m.InstalledByFDH, got.InstalledByFDH)
	assert.Equal(t, m.SourcePath, got.SourcePath)
	assert.Equal(t, m.ContentHash, got.ContentHash)
	assert.Equal(t, m.Agent, got.Agent)
	assert.True(t, m.InstalledAt.Equal(got.InstalledAt), "got %v, want %v", got.InstalledAt, m.InstalledAt)
}

func TestWriteRead_RoundTripFlat(t *testing.T) {
	dir := t.TempDir()
	m := managed.Marker{
		Name:           "review",
		Kind:           "skill",
		InstalledByFDH: "0.7.0",
		SourcePath:     "skills/review",
		Agent:          "copilot",
	}
	path, err := managed.Write(dir, "review.prompt.md", m, true)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "review.prompt.md.fdh-managed.yaml"), path)
	got, err := managed.Read(path)
	require.NoError(t, err)
	assert.Equal(t, "review", got.Name)
	assert.False(t, got.InstalledAt.IsZero(), "auto-populated InstalledAt")
}

func TestMigrate_Directory(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, ".skill-version")
	legacyBody := `name: design-system
hub_version: "0.4.0"
hub_commit: abc123
installed_at: 2026-05-29T10:00:00Z
installed_by_fdh: "0.6.0"
content_hash: sha256:dead
agent: claude-code
`
	require.NoError(t, os.WriteFile(legacy, []byte(legacyBody), 0o644))

	newPath, m, err := managed.Migrate(legacy)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, ".fdh-managed.yaml"), newPath)
	assert.Equal(t, "design-system", m.Name)
	assert.Equal(t, "skill", m.Kind)
	assert.Equal(t, "0.4.0", m.Version)
	assert.Equal(t, "skills/design-system", m.SourcePath)
	// Legacy removed.
	_, statErr := os.Stat(legacy)
	assert.True(t, os.IsNotExist(statErr), "legacy marker should be removed, stat: %v", statErr)
	// New present.
	_, err = os.Stat(newPath)
	assert.NoError(t, err)
}

func TestMigrate_FlatRecoverNameFromFilename(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, ".skill-version-review")
	legacyBody := `hub_version: "0.3.0"
hub_commit: deadbeef
installed_at: 2026-05-29T10:00:00Z
installed_by_fdh: "0.6.0"
agent: copilot
`
	require.NoError(t, os.WriteFile(legacy, []byte(legacyBody), 0o644))

	newPath, m, err := managed.Migrate(legacy)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "review.fdh-managed.yaml"), newPath)
	assert.Equal(t, "review", m.Name, "name recovered from filename")
	assert.Equal(t, "skill", m.Kind)
}

func TestMigrate_BothPresent_NewWinsLegacyRemoved(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, ".skill-version")
	newPath := filepath.Join(dir, ".fdh-managed.yaml")
	require.NoError(t, os.WriteFile(legacy, []byte("name: x\nhub_version: \"0.1.0\"\n"), 0o644))
	require.NoError(t, os.WriteFile(newPath, []byte("name: x\nkind: skill\nversion: \"0.2.0\"\ninstalled_at: 2026-01-01T00:00:00Z\n"), 0o644))

	gotPath, m, err := managed.Migrate(legacy)
	require.NoError(t, err)
	assert.Equal(t, newPath, gotPath)
	assert.Equal(t, "0.2.0", m.Version, "new marker wins")
	_, statErr := os.Stat(legacy)
	assert.True(t, os.IsNotExist(statErr))
}
