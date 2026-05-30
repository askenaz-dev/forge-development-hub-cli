package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// writeTree materializes a {relpath: content} map under root, creating parent
// dirs as needed. Mode is 0644 for all files unless the relpath ends with
// "@exec" (which is stripped and the file is created with 0755).
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		mode := os.FileMode(0o644)
		name := rel
		if strings.HasSuffix(name, "@exec") {
			name = strings.TrimSuffix(name, "@exec")
			mode = 0o755
		}
		full := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), mode))
	}
}

func TestBuildDeterministicTarball_Determinism(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"SKILL.md":           "---\nname: test\ndescription: t\n---\n",
		"scripts/run.sh":     "#!/bin/sh\necho hi\n",
		"references/note.md": "note body\n",
	})

	publishedAt := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)

	first, firstSHA, err := BuildDeterministicTarball(dir, publishedAt)
	require.NoError(t, err)

	for i := 0; i < 9; i++ {
		data, sha, err := BuildDeterministicTarball(dir, publishedAt)
		require.NoError(t, err)
		require.True(t, bytes.Equal(first, data), "iteration %d: bytes differ", i+1)
		require.Equal(t, firstSHA, sha, "iteration %d: sha differs", i+1)
	}

	// And the returned SHA is over the gzipped bytes themselves.
	expectedSum := sha256.Sum256(first)
	require.Equal(t, hex.EncodeToString(expectedSum[:]), firstSHA)
}

func TestBuildDeterministicTarball_MtimeOverride(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"SKILL.md": "body\n",
	})

	a, _, err := BuildDeterministicTarball(dir, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	b, _, err := BuildDeterministicTarball(dir, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.False(t, bytes.Equal(a, b), "different publishedAt should produce different bytes")

	// Zero value collapses to epoch.
	zero1, _, err := BuildDeterministicTarball(dir, time.Time{})
	require.NoError(t, err)
	zero2, _, err := BuildDeterministicTarball(dir, time.Unix(0, 0).UTC())
	require.NoError(t, err)
	require.True(t, bytes.Equal(zero1, zero2), "zero value should equal explicit epoch")
}

func TestBuildDeterministicTarball_ZeroMtimeInHeaders(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"SKILL.md": "x\n"})

	data, _, err := BuildDeterministicTarball(dir, time.Time{})
	require.NoError(t, err)

	gz, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer func() { _ = gz.Close() }()

	// gzip header: Name empty, OS 255, ModTime zero.
	require.Equal(t, "", gz.Name)
	require.Equal(t, byte(255), gz.OS)
	require.True(t, gz.ModTime.IsZero() || gz.ModTime.Equal(time.Unix(0, 0)))

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		require.Equal(t, 0, hdr.Uid)
		require.Equal(t, 0, hdr.Gid)
		require.Equal(t, "", hdr.Uname)
		require.Equal(t, "", hdr.Gname)
		require.True(t, hdr.ModTime.Equal(time.Unix(0, 0).UTC()),
			"entry %s mtime not epoch (got %v)", hdr.Name, hdr.ModTime)
	}
}

func TestBuildDeterministicTarball_ModeNormalization(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec bit not meaningful on Windows")
	}
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"SKILL.md":         "body\n",
		"scripts/run@exec": "#!/bin/sh\n",
	})

	data, _, err := BuildDeterministicTarball(dir, time.Time{})
	require.NoError(t, err)

	modes := readTarModes(t, data)
	require.Equal(t, int64(0o644), modes["SKILL.md"])
	require.Equal(t, int64(0o755), modes["scripts/run"])
	require.Equal(t, int64(0o755), modes["scripts/"])
}

func TestBuildDeterministicTarball_SkipsDotfiles(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"SKILL.md":      "body\n",
		".gitignore":    "*.log\n",
		".meta/info.md": "hidden\n",
	})

	data, _, err := BuildDeterministicTarball(dir, time.Time{})
	require.NoError(t, err)
	names := readTarNames(t, data)
	require.Contains(t, names, "SKILL.md")
	require.NotContains(t, names, ".gitignore")
	for _, n := range names {
		require.False(t, strings.Contains(n, ".meta"),
			"dotfile-prefixed directory %q must be skipped", n)
	}
}

func TestBuildDeterministicTarball_PAXOnLongPath(t *testing.T) {
	dir := t.TempDir()
	// Build a path whose final segment alone exceeds USTAR's 100-byte
	// name limit. USTAR's 155-byte prefix can only split at "/" boundaries,
	// so a 200-char single segment cannot be encoded in USTAR — Go's writer
	// must promote to PAX.
	longSeg := strings.Repeat("x", 120)
	deep := "scripts/" + longSeg + ".txt"
	require.Greater(t, len(deep), 100, "fixture path must exceed USTAR limit")
	writeTree(t, dir, map[string]string{
		"SKILL.md": "body\n",
		deep:       "deep content\n",
	})

	data, _, err := BuildDeterministicTarball(dir, time.Time{})
	require.NoError(t, err)

	// The presence of PAX extended headers is unambiguous: the tar stream
	// contains a typeflag 'x' (or 'g') block. We grep the decompressed bytes
	// for the "PaxHeader" magic the Go writer emits.
	gz, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer func() { _ = gz.Close() }()
	raw, err := io.ReadAll(gz)
	require.NoError(t, err)
	require.True(t, bytes.Contains(raw, []byte("PaxHeader")),
		"long-path tarball must contain PAX extended header magic")

	// Round-trip: the long path is preserved on extract.
	extractDir := t.TempDir()
	require.NoError(t, extractTarGz(bytes.NewReader(data), extractDir))
	_, err = os.Stat(filepath.Join(extractDir, "scripts", longSeg+".txt"))
	require.NoError(t, err, "long path must round-trip through tarball")
}

func TestBuildDeterministicTarball_RejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"SKILL.md": "body\n"})
	target := filepath.Join(dir, "SKILL.md")
	link := filepath.Join(dir, "alias.md")
	require.NoError(t, os.Symlink(target, link))

	_, _, err := BuildDeterministicTarball(dir, time.Time{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported_entry")
	require.Contains(t, err.Error(), "alias.md")
}

func TestBuildDeterministicTarball_NotADirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notadir")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
	_, _, err := BuildDeterministicTarball(file, time.Time{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a directory")
}

func TestBuildDeterministicTarball_RoundTripContentHash(t *testing.T) {
	// The canonical content hash (Bundle.Hash) MUST equal whether computed
	// over the source or over the extracted tarball — that invariant is what
	// the CLI relies on when verifying bundle.sha256.
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"SKILL.md":           "---\nname: hash-roundtrip\ndescription: x\n---\nbody\n",
		"scripts/run.sh":     "echo hi\n",
		"references/note.md": "note\n",
	})

	srcBundle, err := Load(dir)
	require.NoError(t, err)
	srcHash, err := srcBundle.Hash()
	require.NoError(t, err)

	data, _, err := BuildDeterministicTarball(dir, time.Time{})
	require.NoError(t, err)

	// Extract into a fresh dir.
	extractDir := t.TempDir()
	require.NoError(t, extractTarGz(bytes.NewReader(data), extractDir))

	// Bundle.Load expects the bundle directory to be named after the
	// frontmatter name. The tarball preserves relpaths under the root, so
	// the extract destination *is* the bundle root; but Bundle.Load requires
	// DirName == name. Wrap the extract by renaming the temp dir.
	wrapped := filepath.Join(filepath.Dir(extractDir), "hash-roundtrip")
	require.NoError(t, os.Rename(extractDir, wrapped))
	t.Cleanup(func() { _ = os.RemoveAll(wrapped) })

	extractedBundle, err := Load(wrapped)
	require.NoError(t, err)
	extractedHash, err := extractedBundle.Hash()
	require.NoError(t, err)

	require.Equal(t, srcHash, extractedHash,
		"canonical Bundle.Hash must round-trip through the deterministic tarball")
}

// --- test helpers ---

func extractTarGz(r io.Reader, dst string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}

func readTarNames(t *testing.T, data []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		names = append(names, hdr.Name)
	}
	return names
}

func readTarModes(t *testing.T, data []byte) map[string]int64 {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	modes := map[string]int64{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		modes[hdr.Name] = hdr.Mode
	}
	return modes
}
