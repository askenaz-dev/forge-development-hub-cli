package provenance_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/falabella/fdh/pkg/provenance"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteAndReadSidecar(t *testing.T) {
	dir := t.TempDir()
	in := provenance.SkillMeta{
		Registry:         "https://reg.internal",
		Namespace:        "security",
		Name:             "owasp-review",
		Version:          "1.2.0",
		ContentHash:      "abc123",
		InstalledBy:      "alice@host",
		TargetAgents:     []string{"opencode", "claude-code"},
		Scope:            "project",
		Path:             dir,
		InstallerVersion: "dev",
	}
	require.NoError(t, provenance.WriteSidecar(dir, in))

	out, err := provenance.ReadSidecar(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, out.SchemaVersion)
	assert.Equal(t, "owasp-review", out.Name)
	assert.Equal(t, "abc123", out.ContentHash)
	// Agents normalized to sorted order on write.
	assert.Equal(t, []string{"claude-code", "opencode"}, out.TargetAgents)
	assert.NotEmpty(t, out.InstalledAt, "installed_at should be auto-populated")
}

func TestReadSidecar_AbsentReturnsZero(t *testing.T) {
	dir := t.TempDir()
	meta, err := provenance.ReadSidecar(dir)
	require.NoError(t, err)
	assert.Equal(t, 0, meta.SchemaVersion, "absent sidecar -> zero SchemaVersion")
}

func TestReadSidecar_MissingSchemaVersionFails(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".skill-meta.yaml"),
		[]byte("name: x\n"), 0o644))
	_, err := provenance.ReadSidecar(dir)
	require.Error(t, err)
}

func TestInjectBreadcrumb_AddsOnce(t *testing.T) {
	src := []byte("---\nname: x\ndescription: hi\n---\nbody\n")
	ref := "https://reg.internal/ns/x@1.0.0"
	out := provenance.InjectBreadcrumb(src, ref)

	assert.Contains(t, string(out), "installed_from: "+ref)

	// Run again — should still contain exactly one occurrence.
	out2 := provenance.InjectBreadcrumb(out, ref)
	assert.Equal(t, 1, strings.Count(string(out2), "installed_from:"))
}

func TestInjectBreadcrumb_ReplacesExisting(t *testing.T) {
	src := []byte("---\nname: x\ninstalled_from: old-value\ndescription: hi\n---\nbody\n")
	ref := "https://reg.internal/ns/x@2.0.0"
	out := provenance.InjectBreadcrumb(src, ref)
	assert.Contains(t, string(out), "installed_from: "+ref)
	assert.NotContains(t, string(out), "old-value")
}

func TestInjectBreadcrumb_PreservesBodyByteIdentity(t *testing.T) {
	src := []byte("---\nname: roundtrip\ndescription: byte-identity check\nmetadata:\n  author: x\n---\nfirst line\nsecond line\n  indented line\n")
	out := provenance.InjectBreadcrumb(src, "ref/x@1")
	stripped := provenance.StripBreadcrumb(out)
	assert.Equal(t, src, stripped, "stripping the breadcrumb must yield the original byte-for-byte")
}

func TestInjectBreadcrumb_NoFrontmatterInsertsBlock(t *testing.T) {
	src := []byte("body without frontmatter\n")
	out := provenance.InjectBreadcrumb(src, "ref/x@1")
	assert.True(t, bytes.HasPrefix(out, []byte("---\ninstalled_from: ref/x@1\n---\n")))
	assert.Contains(t, string(out), "body without frontmatter")
}

func TestInjectBreadcrumb_CRLFPreserved(t *testing.T) {
	src := []byte("---\r\nname: crlf\r\ndescription: handles windows line endings\r\n---\r\nbody\r\n")
	out := provenance.InjectBreadcrumb(src, "ref/x@1")
	// The newly added line should use CRLF (the dominant ending).
	assert.Contains(t, string(out), "installed_from: ref/x@1\r\n")
	// Original CRLFs preserved.
	assert.Contains(t, string(out), "name: crlf\r\n")
}

func TestInjectBreadcrumb_PreservesByteIdentityWithCRLF(t *testing.T) {
	src := []byte("---\r\nname: crlf\r\ndescription: byte-identity\r\n---\r\nbody line one\r\nbody line two\r\n")
	out := provenance.InjectBreadcrumb(src, "ref/y@2")
	stripped := provenance.StripBreadcrumb(out)
	assert.Equal(t, src, stripped)
}

func TestInjectBreadcrumb_EmptyRefIsNoop(t *testing.T) {
	src := []byte("---\nname: x\n---\nbody\n")
	out := provenance.InjectBreadcrumb(src, "")
	assert.Equal(t, src, out)
}

func TestMakeBreadcrumbRef(t *testing.T) {
	ref := provenance.MakeBreadcrumbRef("https://reg.internal/", "security", "owasp", "1.2.0")
	assert.Equal(t, "https://reg.internal/security/owasp@1.2.0", ref)
}

func TestSidecarPresent(t *testing.T) {
	dir := t.TempDir()
	assert.False(t, provenance.SidecarPresent(dir))
	require.NoError(t, provenance.WriteSidecar(dir, provenance.SkillMeta{
		Registry: "x", Namespace: "n", Name: "s", Version: "1.0.0",
		ContentHash: "h", InstalledBy: "u@h", Scope: "user", Path: dir,
		InstallerVersion: "dev", TargetAgents: []string{"claude-code"},
	}))
	assert.True(t, provenance.SidecarPresent(dir))
}
