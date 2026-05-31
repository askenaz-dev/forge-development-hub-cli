package consumerlock_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/pkg/consumerlock"
	"github.com/forge/fdh/pkg/consumermanifest"
	"github.com/forge/fdh/pkg/hubregistry"
	"github.com/forge/fdh/pkg/managed"
)

func sampleResolved() []consumermanifest.ResolvedComponent {
	return []consumermanifest.ResolvedComponent{
		{
			Name: "design-system", Kind: managed.KindSkill,
			HubEntry: &hubregistry.ComponentEntry{
				Name: "design-system", Kind: managed.KindSkill,
				Path: "skills/design-system", Version: "0.4.0",
			},
		},
		{
			Name: "no-console-log", Kind: managed.KindRule,
			HubEntry: &hubregistry.ComponentEntry{
				Name: "no-console-log", Kind: managed.KindRule,
				Path: "rules/no-console-log", Version: "0.1.0",
			},
		},
	}
}

func TestBuild_GroupsByKind(t *testing.T) {
	l := consumerlock.Build(sampleResolved(), "abc123", time.Unix(1, 0), "default")
	assert.Equal(t, "abc123", l.HubCommit)
	assert.Equal(t, "default", l.ResolvedFromHarness)
	require.Len(t, l.Skills, 1)
	assert.Equal(t, "design-system", l.Skills[0].Name)
	require.Len(t, l.Rules, 1)
	assert.Equal(t, "no-console-log", l.Rules[0].Name)
}

func TestWriteRead_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	l := consumerlock.Build(sampleResolved(), "abc123", time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC), "")
	require.NoError(t, consumerlock.Write(dir, l))
	got, err := consumerlock.Read(dir)
	require.NoError(t, err)
	assert.Equal(t, l.HubCommit, got.HubCommit)
	require.Len(t, got.Skills, 1)
	assert.Equal(t, "design-system", got.Skills[0].Name)
}

func TestWrite_IdempotentBytes(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	at := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	l1 := consumerlock.Build(sampleResolved(), "abc123", at, "minimal")
	l2 := consumerlock.Build(sampleResolved(), "abc123", at, "minimal")
	require.NoError(t, consumerlock.Write(dir1, l1))
	require.NoError(t, consumerlock.Write(dir2, l2))

	b1, err := os.ReadFile(filepath.Join(dir1, ".fdh", "lock.yaml"))
	require.NoError(t, err)
	b2, err := os.ReadFile(filepath.Join(dir2, ".fdh", "lock.yaml"))
	require.NoError(t, err)
	assert.Equal(t, b1, b2, "byte-deterministic output for same input")
}

func TestWrite_OutputLFOnly(t *testing.T) {
	dir := t.TempDir()
	l := consumerlock.Build(sampleResolved(), "abc123", time.Unix(1, 0), "")
	require.NoError(t, consumerlock.Write(dir, l))
	body, err := os.ReadFile(filepath.Join(dir, ".fdh", "lock.yaml"))
	require.NoError(t, err)
	assert.NotContains(t, string(body), "\r\n", "lock must use LF endings")
}

func TestDiff_Missing(t *testing.T) {
	resolved := sampleResolved()
	emptyLock := consumerlock.Empty()
	divs := consumerlock.Diff(resolved, emptyLock, nil)
	require.Len(t, divs, 2)
	for _, d := range divs {
		assert.Equal(t, "missing", d.Status)
	}
}

func TestDiff_Extra(t *testing.T) {
	l := consumerlock.Build(sampleResolved(), "abc", time.Unix(1, 0), "")
	divs := consumerlock.Diff(nil, l, nil)
	require.Len(t, divs, 2)
	for _, d := range divs {
		assert.Equal(t, "extra", d.Status)
	}
}

func TestDiff_IntegrityMismatch(t *testing.T) {
	resolved := sampleResolved()
	l := consumerlock.Build(resolved, "abc", time.Unix(1, 0), "")
	l.Skills[0].Integrity = "X"
	prov := func(name, kind string) (string, bool) {
		if name == "design-system" {
			return "Y", true
		}
		return "", false
	}
	divs := consumerlock.Diff(resolved, l, prov)
	require.Len(t, divs, 1)
	assert.Equal(t, "integrity", divs[0].Status)
}

func TestDiff_NoDivergence(t *testing.T) {
	resolved := sampleResolved()
	l := consumerlock.Build(resolved, "abc", time.Unix(1, 0), "")
	divs := consumerlock.Diff(resolved, l, nil)
	assert.Empty(t, divs)
}

func TestRead_UnsupportedSchemaFails(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".fdh"), 0o755))
	bad := `schema_version: 2
hub_commit: abc
resolved_at: 2026-05-29T00:00:00Z
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".fdh", "lock.yaml"), []byte(bad), 0o644))
	_, err := consumerlock.Read(dir)
	require.Error(t, err)
}

// TestRead_LegacyResolvedFromProfile verifies a pre-rename lock carrying
// `resolved_from_profile` decodes under strict KnownFields and is
// normalized into ResolvedFromHarness.
func TestRead_LegacyResolvedFromProfile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".fdh"), 0o755))
	legacy := `schema_version: 1
hub_commit: abc123
resolved_at: 2026-05-29T00:00:00Z
resolved_from_profile: default
skills:
  - name: design-system
    path: skills/design-system
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".fdh", "lock.yaml"), []byte(legacy), 0o644))
	got, err := consumerlock.Read(dir)
	require.NoError(t, err)
	assert.Equal(t, "default", got.ResolvedFromHarness, "legacy resolved_from_profile must normalize into ResolvedFromHarness")
	assert.Empty(t, got.ResolvedFromProfileLegacy, "legacy field cleared after normalization")
}
