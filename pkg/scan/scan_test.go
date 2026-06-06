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

func TestVerdict(t *testing.T) {
	tests := []struct {
		name     string
		findings []scan.Finding
		want     string
	}{
		{"no findings → pass", nil, scan.StatusPass},
		{"info only → pass", []scan.Finding{{Severity: scan.SeverityInfo}}, scan.StatusPass},
		{"warning → warn", []scan.Finding{{Severity: scan.SeverityWarning}}, scan.StatusWarn},
		{"error → fail", []scan.Finding{{Severity: scan.SeverityError}}, scan.StatusFail},
		{"error dominates warning", []scan.Finding{{Severity: scan.SeverityWarning}, {Severity: scan.SeverityError}}, scan.StatusFail},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, scan.Verdict(&scan.Result{Findings: tc.findings}))
		})
	}
}

func TestDirStatus(t *testing.T) {
	t.Run("clean dir → pass", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "a.md", "# hello\n")
		got, err := scan.DirStatus(dir)
		require.NoError(t, err)
		assert.Equal(t, scan.StatusPass, got)
	})

	t.Run("blocking finding → fail", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "x.md", "token: ghp_abcdefghijklmnopqrstuvwxyz1234567890\n")
		got, err := scan.DirStatus(dir)
		require.NoError(t, err)
		assert.Equal(t, scan.StatusFail, got)
	})

	t.Run("non-blocking finding → warn", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "x.md", "jwt: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N\n")
		got, err := scan.DirStatus(dir)
		require.NoError(t, err)
		assert.Equal(t, scan.StatusWarn, got)
	})

	t.Run("unreadable path → none + error", func(t *testing.T) {
		got, err := scan.DirStatus(filepath.Join(t.TempDir(), "does-not-exist"))
		require.Error(t, err)
		assert.Equal(t, scan.StatusNone, got)
	})
}
