package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// Hash computes the bundle's canonical SHA-256 digest.
//
// The algorithm is the one fixed in design.md §"Canonical bundle hash":
//
//  1. List every file with a relative path using forward slashes.
//  2. Sort the file list lexicographically (already done by Load).
//  3. For each file emit "<mode> <sha256> <relpath>\n", where <mode> is the
//     canonical "100644" / "100755" string.
//  4. The bundle hash is SHA-256 of the concatenated lines, lowercase hex.
//
// The digest is computed over the source bundle as published. Install-time
// modifications (notably the installed_from frontmatter breadcrumb) MUST be
// applied AFTER hashing so the registry-recorded hash equals the install
// sidecar's content_hash regardless of where the bundle lives.
func (b *Bundle) Hash() (string, error) {
	if len(b.Files) == 0 {
		return "", fmt.Errorf("bundle has no files to hash")
	}

	var sb strings.Builder
	for _, f := range b.Files {
		// One line per file. The trailing newline is significant — it is
		// part of the canonical encoding.
		fmt.Fprintf(&sb, "%s %s %s\n", f.Mode, f.ContentSHA256, f.RelPath)
	}

	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:]), nil
}

// HashDir computes the canonical content hash of srcDir without requiring
// a SKILL.md entrypoint or frontmatter validation. It is the loader-free
// equivalent of (*Bundle).Hash and produces the same digest as long as the
// underlying file set is the same.
//
// Wire-protocol handlers use this for non-skill kinds (rules/agents/hooks)
// whose entrypoint files are named per kind (RULE.md, AGENT.md, HOOK.md)
// and would fail bundle.Load's SKILL.md requirement.
func HashDir(srcDir string) (string, error) {
	abs, err := os.Stat(srcDir)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", srcDir, err)
	}
	if !abs.IsDir() {
		return "", fmt.Errorf("not a directory: %s", srcDir)
	}
	files, err := walkFiles(srcDir)
	if err != nil {
		return "", fmt.Errorf("walk: %w", err)
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no files to hash in %s", srcDir)
	}
	var sb strings.Builder
	for _, f := range files {
		fmt.Fprintf(&sb, "%s %s %s\n", f.Mode, f.ContentSHA256, f.RelPath)
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:]), nil
}

// sha256File reads the file at path and returns the lowercase hex SHA-256.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
