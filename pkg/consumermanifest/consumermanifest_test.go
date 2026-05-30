package consumermanifest_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/pkg/consumermanifest"
	"github.com/forge/fdh/pkg/hubregistry"
	"github.com/forge/fdh/pkg/managed"
)

func TestLoad_ValidManifest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".fdh"), 0o755))
	body := `schema_version: 1
profile: minimal
skills:
  - name: design-system
rules:
  - name: no-console-log
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".fdh", "manifest.yaml"), []byte(body), 0o644))

	m, err := consumermanifest.Load(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, m.SchemaVersion)
	assert.Equal(t, "minimal", m.Profile)
	require.Len(t, m.Skills, 1)
	assert.Equal(t, "design-system", m.Skills[0].Name)
	require.Len(t, m.Rules, 1)
	assert.Equal(t, "no-console-log", m.Rules[0].Name)
}

func TestLoad_UnknownFieldFails(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".fdh"), 0o755))
	body := `schema_version: 1
mystery_field: foo
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".fdh", "manifest.yaml"), []byte(body), 0o644))

	_, err := consumermanifest.Load(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mystery_field")
}

func TestValidate_UnsupportedSchema(t *testing.T) {
	m := &consumermanifest.Manifest{SchemaVersion: 2}
	err := consumermanifest.Validate(m)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema_version 2")
}

func TestValidate_InvalidEntryName(t *testing.T) {
	m := &consumermanifest.Manifest{
		SchemaVersion: 1,
		Skills:        []consumermanifest.Entry{{Name: ""}},
	}
	err := consumermanifest.Validate(m)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing 'name'")
}

func TestValidate_InvalidScope(t *testing.T) {
	m := &consumermanifest.Manifest{SchemaVersion: 1, Scope: "global"}
	err := consumermanifest.Validate(m)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope")
}

func TestExpand_ExplicitEntriesResolve(t *testing.T) {
	reg := &hubregistry.Registry{
		Components: []hubregistry.ComponentEntry{
			{Name: "design-system", Kind: managed.KindSkill, Path: "skills/design-system", AgentsSupported: []string{"claude-code"}},
			{Name: "no-console-log", Kind: managed.KindRule, Path: "rules/no-console-log", AgentsSupported: []string{"claude-code"}},
		},
	}
	m := &consumermanifest.Manifest{
		SchemaVersion: 1,
		Skills:        []consumermanifest.Entry{{Name: "design-system"}},
		Rules:         []consumermanifest.Entry{{Name: "no-console-log"}},
	}
	resolved, err := consumermanifest.Expand(m, reg, nil)
	require.NoError(t, err)
	require.Len(t, resolved, 2)
	// Sorted by (kind, name): rule before skill alphabetically.
	assert.Equal(t, managed.KindRule, resolved[0].Kind)
	assert.Equal(t, "no-console-log", resolved[0].Name)
	assert.Equal(t, managed.KindSkill, resolved[1].Kind)
}

func TestExpand_ProfileAndExtends(t *testing.T) {
	reg := &hubregistry.Registry{
		Components: []hubregistry.ComponentEntry{
			{Name: "design-system", Kind: managed.KindSkill, Path: "skills/design-system", AgentsSupported: []string{"claude-code"}},
			{Name: "code-review", Kind: managed.KindSkill, Path: "skills/code-review", AgentsSupported: []string{"claude-code"}},
			{Name: "extra", Kind: managed.KindSkill, Path: "skills/extra", AgentsSupported: []string{"claude-code"}},
		},
	}
	m := &consumermanifest.Manifest{
		SchemaVersion: 1,
		Profile:       "minimal",
		Extends: &consumermanifest.Extends{
			AddSkills:    []consumermanifest.Entry{{Name: "extra"}},
			RemoveSkills: []consumermanifest.Entry{{Name: "design-system"}},
		},
	}
	profile := func(name string) ([]consumermanifest.ProfileMember, error) {
		if name != "minimal" {
			return nil, errors.New("unknown profile")
		}
		return []consumermanifest.ProfileMember{
			{Name: "design-system", Kind: managed.KindSkill},
			{Name: "code-review", Kind: managed.KindSkill},
		}, nil
	}
	resolved, err := consumermanifest.Expand(m, reg, profile)
	require.NoError(t, err)
	names := make([]string, 0, len(resolved))
	for _, r := range resolved {
		names = append(names, r.Name)
	}
	assert.ElementsMatch(t, []string{"code-review", "extra"}, names)
}

func TestExpand_UnresolvedComponentFails(t *testing.T) {
	reg := &hubregistry.Registry{}
	m := &consumermanifest.Manifest{
		SchemaVersion: 1,
		Skills:        []consumermanifest.Entry{{Name: "missing"}},
	}
	_, err := consumermanifest.Expand(m, reg, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}

func TestExpand_ProfileNotResolvableFails(t *testing.T) {
	reg := &hubregistry.Registry{}
	m := &consumermanifest.Manifest{SchemaVersion: 1, Profile: "minimal"}
	_, err := consumermanifest.Expand(m, reg, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "profile")
}

func TestWrite_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := &consumermanifest.Manifest{
		SchemaVersion: 1,
		Profile:       "minimal",
		Skills:        []consumermanifest.Entry{{Name: "design-system"}},
	}
	require.NoError(t, consumermanifest.Write(dir, m))
	got, err := consumermanifest.Load(dir)
	require.NoError(t, err)
	assert.Equal(t, m.Profile, got.Profile)
	assert.Len(t, got.Skills, 1)
}

func TestGenerateFromLegacy_FindsMarkers(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".claude", "skills", "design-system")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	body := `name: design-system
kind: skill
installed_at: 2026-05-29T00:00:00Z
`
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, managed.Filename), []byte(body), 0o644))

	m, err := consumermanifest.GenerateFromLegacy(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, m.SchemaVersion)
	require.Len(t, m.Skills, 1)
	assert.Equal(t, "design-system", m.Skills[0].Name)
}

func TestGenerateFromLegacy_FromLegacyMarker(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".claude", "skills", "design-system")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	legacy := `name: design-system
hub_version: "0.4.0"
installed_at: 2026-05-29T00:00:00Z
`
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, ".skill-version"), []byte(legacy), 0o644))

	m, err := consumermanifest.GenerateFromLegacy(dir)
	require.NoError(t, err)
	require.Len(t, m.Skills, 1)
	assert.Equal(t, "design-system", m.Skills[0].Name)
}

func TestHasAnyEntries(t *testing.T) {
	empty := &consumermanifest.Manifest{SchemaVersion: 1}
	assert.False(t, consumermanifest.HasAnyEntries(empty))

	withProfile := &consumermanifest.Manifest{SchemaVersion: 1, Profile: "x"}
	assert.True(t, consumermanifest.HasAnyEntries(withProfile))

	withSkill := &consumermanifest.Manifest{SchemaVersion: 1, Skills: []consumermanifest.Entry{{Name: "x"}}}
	assert.True(t, consumermanifest.HasAnyEntries(withSkill))
}

// Compile-time check: Manifest can hold all kinds.
var _ = fmt.Sprintf("%T", consumermanifest.Manifest{})
