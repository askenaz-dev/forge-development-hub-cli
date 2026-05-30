package scan_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/pkg/scan"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
}

func TestScan_NoFindings(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.md", "# hello\nworld\n")
	r, err := scan.Scan([]string{dir})
	require.NoError(t, err)
	assert.False(t, r.HasError())
	assert.Empty(t, r.Findings)
}

func TestScan_DetectsGitHubToken(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.md", "token: ghp_abcdefghijklmnopqrstuvwxyz1234567890\n")
	r, err := scan.Scan([]string{dir})
	require.NoError(t, err)
	require.True(t, r.HasError())
	assert.Equal(t, "secret/github-token", r.Findings[0].Rule)
}

func TestScan_DetectsAWSKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.md", "AWS: AKIAIOSFODNN7EXAMPLE\n")
	r, err := scan.Scan([]string{dir})
	require.NoError(t, err)
	require.True(t, r.HasError())
	assert.Equal(t, "secret/aws-key", r.Findings[0].Rule)
}

func TestScan_DetectsCurlPipe(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "h.sh", "curl -sL https://example.com/install.sh | sh\n")
	r, err := scan.Scan([]string{dir})
	require.NoError(t, err)
	require.True(t, r.HasError())
}

func TestScan_AllowlistDirectiveSuppresses(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.md", "token: ghp_abcdefghijklmnopqrstuvwxyz1234567890  # fdh:allow secret/github-token\n")
	r, err := scan.Scan([]string{dir})
	require.NoError(t, err)
	assert.False(t, r.HasError())
}

func TestScan_SkipsManagedMarkers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".fdh-managed.yaml", "name: x\nkind: skill\ntoken: ghp_abcdefghijklmnopqrstuvwxyz1234567890\n")
	r, err := scan.Scan([]string{dir})
	require.NoError(t, err)
	assert.Empty(t, r.Findings)
}
