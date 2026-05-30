package gitignore_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/pkg/gitignore"
)

func TestApply_CreatesFile_WhenNoneExisted(t *testing.T) {
	root := t.TempDir()
	err := gitignore.Apply(root, []string{".claude/skills/design-system/"})
	require.NoError(t, err)

	body, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	require.NoError(t, err)
	assert.Contains(t, string(body), gitignore.MarkerBegin)
	assert.Contains(t, string(body), gitignore.MarkerEnd)
	assert.Contains(t, string(body), ".claude/skills/design-system/")
}

func TestApply_PreservesForeignContent(t *testing.T) {
	root := t.TempDir()
	original := "node_modules/\ndist/\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte(original), 0o644))

	err := gitignore.Apply(root, []string{".claude/skills/x/"})
	require.NoError(t, err)

	body, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	require.NoError(t, err)
	str := string(body)
	assert.Contains(t, str, "node_modules/")
	assert.Contains(t, str, "dist/")
	assert.Contains(t, str, ".claude/skills/x/")
}

func TestApply_Idempotent(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, gitignore.Apply(root, []string{".claude/skills/x/"}))
	path := filepath.Join(root, ".gitignore")
	stat1, err := os.Stat(path)
	require.NoError(t, err)
	body1, err := os.ReadFile(path)
	require.NoError(t, err)

	require.NoError(t, gitignore.Apply(root, []string{".claude/skills/x/"}))
	stat2, err := os.Stat(path)
	require.NoError(t, err)
	body2, err := os.ReadFile(path)
	require.NoError(t, err)

	// Byte-identical and mtime unchanged.
	assert.Equal(t, body1, body2)
	assert.Equal(t, stat1.ModTime(), stat2.ModTime())
}

func TestApply_EmptyPathsRemovesSection(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, gitignore.Apply(root, []string{".claude/skills/x/"}))

	// Append foreign content after the section.
	pre, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	require.NoError(t, err)
	pre = append(pre, []byte("foreign-after/\n")...)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), pre, 0o644))

	// Now strip the section.
	require.NoError(t, gitignore.Apply(root, nil))
	body, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	require.NoError(t, err)
	str := string(body)
	assert.NotContains(t, str, gitignore.MarkerBegin)
	assert.NotContains(t, str, gitignore.MarkerEnd)
	assert.Contains(t, str, "foreign-after/")
}

func TestApply_FDHPathAddsNegations(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, gitignore.Apply(root, []string{".fdh/cache/", ".claude/skills/x/"}))
	body, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	require.NoError(t, err)
	str := string(body)
	assert.Contains(t, str, ".fdh/cache/")
	assert.Contains(t, str, "!.fdh/manifest.yaml")
	assert.Contains(t, str, "!.fdh/lock.yaml")
}

func TestApply_NoFDHPathSkipsNegations(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, gitignore.Apply(root, []string{".claude/skills/x/"}))
	body, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	require.NoError(t, err)
	assert.NotContains(t, string(body), "!.fdh/manifest.yaml")
}

func TestApply_MalformedSectionReturnsError(t *testing.T) {
	root := t.TempDir()
	body := "node_modules/\n" + gitignore.MarkerBegin + "\n.claude/skills/x/\n" // missing end
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte(body), 0o644))
	err := gitignore.Apply(root, []string{".claude/skills/y/"})
	require.Error(t, err)
	assert.ErrorIs(t, err, gitignore.ErrMalformedSection)
}

func TestApply_UpdatesExistingSectionInPlace(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, gitignore.Apply(root, []string{".claude/skills/a/"}))

	// Add foreign content after the section.
	pre, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	require.NoError(t, err)
	pre = append(pre, []byte("foreign-after/\n")...)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), pre, 0o644))

	// Replace section content.
	require.NoError(t, gitignore.Apply(root, []string{".claude/skills/b/"}))

	body, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	require.NoError(t, err)
	str := string(body)
	assert.Contains(t, str, ".claude/skills/b/")
	assert.NotContains(t, str, ".claude/skills/a/")
	assert.Contains(t, str, "foreign-after/")
}

func TestRead_ReportsManagedAndForeign(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"),
		[]byte("node_modules/\n"+
			gitignore.MarkerBegin+"\n"+
			".claude/skills/x/\n"+
			gitignore.MarkerEnd+"\n"+
			"dist/\n"),
		0o644))

	managed, foreign, err := gitignore.Read(root)
	require.NoError(t, err)
	assert.Equal(t, []string{".claude/skills/x/"}, managed)
	assert.Contains(t, foreign, "node_modules/")
	assert.Contains(t, foreign, "dist/")
	assert.NotContains(t, foreign, ".claude/skills/x/")
}
