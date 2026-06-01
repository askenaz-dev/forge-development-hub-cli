package registry_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/pkg/registry"
)

// hubRegistryYAMLFixture is a minimal but realistic v2 catalog covering
// all four kinds, mixed default flags, varied owner_team patterns (so
// the namespace derivation is exercised), and an entry with no tags.
const hubRegistryYAMLFixture = `# v2 catalog fixture
schema_version: 2
hub_version: "2026.05"

components:
  - name: design-system
    kind: skill
    description: Forge design system rules and tokens.
    owner_team: design-platform
    tags: [ui, react]
    default: true
    min_fdh_version: "0.4.0"
    agents_supported: [claude-code, codex, copilot, opencode]
    path: skills/design-system

  - name: no-hardcoded-secrets
    kind: rule
    description: No committed secrets.
    owner_team: appsec
    tags: [security, secrets]
    default: true
    min_fdh_version: "0.4.0"
    agents_supported: [claude-code, codex, copilot, opencode]
    path: rules/no-hardcoded-secrets

  - name: forge-pr-writer
    kind: agent
    description: PR descriptions in forge style.
    owner_team: dx-platform
    default: true
    min_fdh_version: "0.4.0"
    agents_supported: [claude-code]
    path: agents/forge-pr-writer

  - name: doctor-on-session-start
    kind: hook
    description: Run fdh doctor at SessionStart.
    owner_team: dx-platform
    tags: [doctor]
    default: true
    min_fdh_version: "0.4.0"
    agents_supported: [claude-code]
    path: hooks/doctor-on-session-start
`

// writeHubYAMLFixture creates <root>/hub/registry.yaml with the v2 fixture.
func writeHubYAMLFixture(t *testing.T, root string) {
	t.Helper()
	hubDir := filepath.Join(root, "hub")
	require.NoError(t, os.MkdirAll(hubDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(hubDir, "registry.yaml"),
		[]byte(hubRegistryYAMLFixture), 0o644))
}

// When only hub/registry.yaml exists, Index synthesizes an Index from
// the YAML catalog. SchemaVersion is 2, every kind is represented, the
// Skills derived view contains only kind=skill entries, and namespaces
// are derived from owner_team via the canonical rule.
func TestGitRegistry_Index_YAMLFallback_ReturnsAllKinds(t *testing.T) {
	root := t.TempDir()
	writeHubYAMLFixture(t, root)

	r := &registry.GitRegistry{LocalPath: root, SkipFetch: true}
	idx, err := r.Index(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 2, idx.SchemaVersion)
	assert.Equal(t, "git+yaml", idx.Registry)
	assert.Len(t, idx.Components, 4, "all 4 kinds represented")
	assert.Len(t, idx.Skills, 1, "Skills view filtered to kind=skill")

	type key struct{ kind, ns, name string }
	got := map[key]registry.IndexEntry{}
	for _, e := range idx.Components {
		got[key{e.Kind, e.Namespace, e.Name}] = e
	}
	assert.Contains(t, got, key{"skill", "design-platform", "design-system"})
	assert.Contains(t, got, key{"rule", "appsec", "no-hardcoded-secrets"})
	assert.Contains(t, got, key{"agent", "dx-platform", "forge-pr-writer"})
	assert.Contains(t, got, key{"hook", "dx-platform", "doctor-on-session-start"})

	// Owner team flows through; wire sentinels populated.
	skill := got[key{"skill", "design-platform", "design-system"}]
	assert.Equal(t, "design-platform", skill.OwnerTeam)
	assert.Equal(t, "latest", skill.LatestVersion)
	assert.Equal(t, "", skill.LatestHash)
	assert.Equal(t, "none", skill.ScanStatus)
	assert.Equal(t, []string{"ui", "react"}, skill.Tags)
}

// When both index.json and hub/registry.yaml exist, index.json wins —
// the portal-emitted wire format is authoritative when present.
func TestGitRegistry_Index_PrefersJSONOverYAML(t *testing.T) {
	root := t.TempDir()
	// index.json with a SINGLE distinguishable component.
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "index.json"),
		[]byte(`{"schema_version":2,"registry":"json-wire","components":[{"kind":"skill","namespace":"json-team","name":"json-only","description":"x","owner_team":"json-team","latest_version":"1.0.0","latest_hash":"abc","scan_status":"pass"}]}`),
		0o644))
	// hub/registry.yaml with FOUR components — must be ignored.
	writeHubYAMLFixture(t, root)

	r := &registry.GitRegistry{LocalPath: root, SkipFetch: true}
	idx, err := r.Index(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "json-wire", idx.Registry)
	assert.Len(t, idx.Components, 1, "JSON wins; YAML ignored")
	assert.Equal(t, "json-only", idx.Components[0].Name)
}

// When neither file exists, Index returns a clear, actionable error
// naming both paths it looked at.
func TestGitRegistry_Index_NoCatalog(t *testing.T) {
	root := t.TempDir()
	r := &registry.GitRegistry{LocalPath: root, SkipFetch: true}
	_, err := r.Index(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no catalog found")
	assert.Contains(t, err.Error(), "index.json")
	assert.Contains(t, err.Error(), "hub/registry.yaml")
}

// schema_version != 2 in hub/registry.yaml is rejected with a clear
// message — protects against quietly serving a v1 mirror via a path
// that's only ever been wired for v2.
func TestGitRegistry_Index_YAMLFallback_RejectsNonV2(t *testing.T) {
	root := t.TempDir()
	hubDir := filepath.Join(root, "hub")
	require.NoError(t, os.MkdirAll(hubDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(hubDir, "registry.yaml"),
		[]byte("schema_version: 1\nhub_version: \"x\"\ncomponents: []\n"),
		0o644))
	r := &registry.GitRegistry{LocalPath: root, SkipFetch: true}
	_, err := r.Index(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema_version=1")
	assert.Contains(t, err.Error(), "schema_version=2 only")
}

// Search delegates to Index, so it must also work via the YAML
// fallback — proves the public Registry interface is fully usable
// against a hub clone with no index.json.
func TestGitRegistry_Search_YAMLFallback(t *testing.T) {
	root := t.TempDir()
	writeHubYAMLFixture(t, root)
	r := &registry.GitRegistry{LocalPath: root, SkipFetch: true}
	results, err := r.Search(context.Background(), "design")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "design-system", results[0].Name)
	assert.Equal(t, "design-platform", results[0].Namespace)
}

// The canonical namespace rule is exercised by the catalog conversion;
// covers the cases the portal already tests in namespace_internal_test.go.
func TestGitRegistry_Index_NamespaceDerivation(t *testing.T) {
	const yaml = `schema_version: 2
hub_version: "x"
components:
  - {name: a, kind: skill, description: a, owner_team: "Design Platform",      path: skills/a}
  - {name: b, kind: skill, description: b, owner_team: "dx_platform",          path: skills/b}
  - {name: c, kind: skill, description: c, owner_team: "  --weird-team-- ",    path: skills/c}
  - {name: d, kind: skill, description: d, owner_team: "",                     path: skills/d}
  - {name: e, kind: skill, description: e, owner_team: "Team@123!#",           path: skills/e}
`
	root := t.TempDir()
	hubDir := filepath.Join(root, "hub")
	require.NoError(t, os.MkdirAll(hubDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hubDir, "registry.yaml"), []byte(yaml), 0o644))
	r := &registry.GitRegistry{LocalPath: root, SkipFetch: true}
	idx, err := r.Index(context.Background())
	require.NoError(t, err)

	by := map[string]string{}
	for _, e := range idx.Components {
		by[e.Name] = e.Namespace
	}
	// "Design Platform" → spaces stripped (not in [a-z0-9_-]) → "designplatform".
	assert.Equal(t, "designplatform", by["a"])
	assert.Equal(t, "dx-platform", by["b"]) // underscore → dash
	assert.Equal(t, "weird-team", by["c"])  // trim outer dashes
	assert.Equal(t, "unknown", by["d"])     // empty falls back
	assert.Equal(t, "team123", by["e"])     // strip non-[a-z0-9-]
}
