package bundle_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/pkg/bundle"
)

const validSkillMD = `---
name: my-skill
description: A short description for the test fixture.
metadata:
  author: test
---

# Body

Hello world.
`

func writeBundle(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	return dir
}

func TestLoad_ValidBundle(t *testing.T) {
	root := writeBundle(t, map[string]string{
		"my-skill/SKILL.md": validSkillMD,
	})
	b, err := bundle.Load(filepath.Join(root, "my-skill"))
	require.NoError(t, err)
	assert.Equal(t, "my-skill", b.DirName)
	assert.Equal(t, "my-skill", b.SkillMD.Name)
	assert.NotEmpty(t, b.SkillMD.Description)
	assert.True(t, b.SkillMD.IsPortable(), "default portability should be true")
	assert.Len(t, b.Files, 1)
}

func TestLoad_MissingSKILLMD(t *testing.T) {
	root := writeBundle(t, map[string]string{
		"my-skill/notskill.md": "hi",
	})
	_, err := bundle.Load(filepath.Join(root, "my-skill"))
	require.Error(t, err)
}

func TestValidate_NameDirectoryMismatch(t *testing.T) {
	root := writeBundle(t, map[string]string{
		"wrong-dir/SKILL.md": validSkillMD,
	})
	b, err := bundle.Load(filepath.Join(root, "wrong-dir"))
	require.NoError(t, err)
	err = b.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match bundle directory")
}

func TestValidate_BadName(t *testing.T) {
	root := writeBundle(t, map[string]string{
		"Bad_Name/SKILL.md": `---
name: Bad_Name
description: anything
---
body`,
	})
	b, err := bundle.Load(filepath.Join(root, "Bad_Name"))
	require.NoError(t, err)
	err = b.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match")
}

func TestValidate_MissingDescription(t *testing.T) {
	root := writeBundle(t, map[string]string{
		"my-skill/SKILL.md": `---
name: my-skill
---
body`,
	})
	b, err := bundle.Load(filepath.Join(root, "my-skill"))
	require.NoError(t, err)
	err = b.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "description is required")
}

func TestValidate_OptionalSubdirAsFile(t *testing.T) {
	root := writeBundle(t, map[string]string{
		"my-skill/SKILL.md": validSkillMD,
		"my-skill/scripts":  "this is a file, not a dir",
	})
	b, err := bundle.Load(filepath.Join(root, "my-skill"))
	require.NoError(t, err)
	err = b.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scripts")
}

func TestValidate_HappyPath(t *testing.T) {
	root := writeBundle(t, map[string]string{
		"my-skill/SKILL.md":            validSkillMD,
		"my-skill/scripts/run.sh":      "#!/bin/sh\necho hi\n",
		"my-skill/references/api.md":   "# API\n",
		"my-skill/assets/template.txt": "tmpl\n",
	})
	b, err := bundle.Load(filepath.Join(root, "my-skill"))
	require.NoError(t, err)
	require.NoError(t, b.Validate())
}

func TestHash_DeterministicForSameInput(t *testing.T) {
	root := writeBundle(t, map[string]string{
		"my-skill/SKILL.md":          validSkillMD,
		"my-skill/scripts/run.sh":    "#!/bin/sh\necho hi\n",
		"my-skill/references/api.md": "# API\n",
	})
	b1, err := bundle.Load(filepath.Join(root, "my-skill"))
	require.NoError(t, err)
	h1, err := b1.Hash()
	require.NoError(t, err)

	b2, err := bundle.Load(filepath.Join(root, "my-skill"))
	require.NoError(t, err)
	h2, err := b2.Hash()
	require.NoError(t, err)

	assert.Equal(t, h1, h2)
	assert.Len(t, h1, 64, "SHA-256 hex digest is 64 chars")
}

// TestHash_ExpectedDigest pins the canonical hash for a known small bundle.
// Any change to the hashing algorithm will break this test, which is
// intentional — the digest is part of the registry's stored data.
//
// The fixture below is constructed entirely in code so the test is
// independent of any file fixture on disk. The expected hash is computed
// once by hand from the algorithm and pinned here.
func TestHash_ExpectedDigest(t *testing.T) {
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, "fixture")
	require.NoError(t, os.MkdirAll(bundleDir, 0o755))
	// LF line endings, no trailing whitespace.
	require.NoError(t, os.WriteFile(
		filepath.Join(bundleDir, "SKILL.md"),
		[]byte("---\nname: fixture\ndescription: pinned hash test\n---\nbody\n"),
		0o644,
	))

	b, err := bundle.Load(bundleDir)
	require.NoError(t, err)

	got, err := b.Hash()
	require.NoError(t, err)

	// On Unix, the file mode is canonicalized to 100644 (no exec bit).
	// On Windows, files are always 100644 in our canonical mode.
	// Therefore the digest is identical across OSes for this fixture.
	const expected = "ad4b1dfd85bc58be223ced885172911f2cf4eea2a23d1fb9ceafeae41cecc890"
	assert.Equal(t, expected, got, "canonical hash drifted; update the algorithm OR the pinned value (not both casually)")
}

func TestHash_ChangesWhenContentChanges(t *testing.T) {
	root := writeBundle(t, map[string]string{
		"my-skill/SKILL.md": validSkillMD,
	})
	b1, err := bundle.Load(filepath.Join(root, "my-skill"))
	require.NoError(t, err)
	h1, err := b1.Hash()
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(root, "my-skill", "SKILL.md"),
		[]byte(validSkillMD+"\nedit\n"), 0o644))

	b2, err := bundle.Load(filepath.Join(root, "my-skill"))
	require.NoError(t, err)
	h2, err := b2.Hash()
	require.NoError(t, err)

	assert.NotEqual(t, h1, h2)
}

func TestHash_HiddenFilesExcluded(t *testing.T) {
	// .skill-meta.yaml is written next to an installed SKILL.md but is NOT
	// part of the source bundle. Load + Hash must ignore dotfiles so an
	// installed directory hashes identically to its source bundle.
	root := writeBundle(t, map[string]string{
		"my-skill/SKILL.md":         validSkillMD,
		"my-skill/.skill-meta.yaml": "schema_version: 1\n",
	})
	b, err := bundle.Load(filepath.Join(root, "my-skill"))
	require.NoError(t, err)
	require.Len(t, b.Files, 1, ".skill-meta.yaml must be excluded from Files")
	_, err = b.Hash()
	require.NoError(t, err)
}

func TestParseSkillMD_NoFrontmatter(t *testing.T) {
	doc, err := bundle.ParseSkillMD([]byte("just a body\nno frontmatter\n"))
	require.NoError(t, err)
	assert.False(t, doc.HasFrontmatter)
	assert.Empty(t, doc.Name)
	assert.True(t, doc.IsPortable(), "default portability is true even without frontmatter")
}

func TestParseSkillMD_CRLFLineEndings(t *testing.T) {
	in := "---\r\nname: with-crlf\r\ndescription: handles windows line endings\r\n---\r\nbody\r\n"
	doc, err := bundle.ParseSkillMD([]byte(in))
	require.NoError(t, err)
	assert.True(t, doc.HasFrontmatter)
	assert.Equal(t, "with-crlf", doc.Name)
	assert.Equal(t, "handles windows line endings", doc.Description)
}

func TestParseSkillMD_PortableExplicitFalse(t *testing.T) {
	in := `---
name: claude-only-skill
description: a skill that needs Claude
portable: false
compatibility:
  - claude-code
---
body
`
	doc, err := bundle.ParseSkillMD([]byte(in))
	require.NoError(t, err)
	assert.NotNil(t, doc.Portable)
	assert.False(t, doc.IsPortable())
	assert.Equal(t, []string{"claude-code"}, doc.Compatibility)
}

func TestCanonicalMode_RespectsExecBitOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec bit not meaningful on Windows")
	}
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, "exec-fixture")
	require.NoError(t, os.MkdirAll(filepath.Join(bundleDir, "scripts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bundleDir, "SKILL.md"),
		[]byte("---\nname: exec-fixture\ndescription: test exec bit\n---\nbody\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(bundleDir, "scripts/run.sh"),
		[]byte("#!/bin/sh\n"), 0o755))

	b, err := bundle.Load(bundleDir)
	require.NoError(t, err)

	var execMode string
	for _, f := range b.Files {
		if f.RelPath == "scripts/run.sh" {
			execMode = f.Mode
		}
	}
	assert.Equal(t, "100755", execMode)
}
