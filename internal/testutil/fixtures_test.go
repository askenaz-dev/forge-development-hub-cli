package testutil_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/falabella/fdh/pkg/bundle"
	"github.com/falabella/fdh/pkg/portability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sanity that the static on-disk fixtures parse and lint per expectation.
func TestFixture_Portable(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	root := filepath.Dir(here)
	b, err := bundle.Load(filepath.Join(root, "fixtures", "portable-skill"))
	require.NoError(t, err)
	require.NoError(t, b.Validate())
	findings := portability.Lint(b, portability.LintOptions{})
	assert.Empty(t, findings)
}

func TestFixture_ClaudeOnly(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	root := filepath.Dir(here)
	b, err := bundle.Load(filepath.Join(root, "fixtures", "claude-only-skill"))
	require.NoError(t, err)
	require.NoError(t, b.Validate())
	findings := portability.Lint(b, portability.LintOptions{
		KnownAgentIDs: []string{"claude-code", "copilot", "codex", "opencode"},
	})
	// Non-portable skills with declared compatibility must NOT fail the lint.
	assert.Empty(t, findings)
}

func TestFixture_PortableLeakageDetected(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	root := filepath.Dir(here)
	b, err := bundle.Load(filepath.Join(root, "fixtures", "portable-with-claude-leakage"))
	require.NoError(t, err)
	require.NoError(t, b.Validate())
	findings := portability.Lint(b, portability.LintOptions{})
	require.NotEmpty(t, findings)
	rules := map[string]bool{}
	for _, f := range findings {
		rules[f.RuleID] = true
	}
	assert.True(t, rules["PORT200"], "expected PORT200 on portable-with-claude-leakage fixture")
}

func TestFixture_WithScripts(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	root := filepath.Dir(here)
	b, err := bundle.Load(filepath.Join(root, "fixtures", "with-scripts"))
	require.NoError(t, err)
	require.NoError(t, b.Validate())

	// Files should include both the script and the reference.
	rels := map[string]bool{}
	for _, f := range b.Files {
		rels[f.RelPath] = true
	}
	assert.True(t, rels["scripts/hello.sh"])
	assert.True(t, rels["references/notes.md"])
}
