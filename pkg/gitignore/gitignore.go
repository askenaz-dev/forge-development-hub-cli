// Package gitignore manages a sectionned, idempotent block inside the
// consumer repo's `.gitignore` that lists the paths `fdh` owns. The
// block is delimited by sentinels:
//
//	# >>> fdh:managed-paths >>>
//	<managed paths, one per line>
//	# <<< fdh:managed-paths <<<
//
// Content outside the block is preserved byte-identically.
//
// Boundaries: stdlib only. MUST NOT import pkg/managed, pkg/adapters
// or internal/cli.
package gitignore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	MarkerBegin = "# >>> fdh:managed-paths >>>"
	MarkerEnd   = "# <<< fdh:managed-paths <<<"

	gitignoreFilename = ".gitignore"

	// negationManifest / negationLock are added inside the managed
	// block when any proposed path overlaps the `.fdh/` directory, so
	// the manifest+lock never end up gitignored even by accident.
	negationManifest = "!.fdh/manifest.yaml"
	negationLock     = "!.fdh/lock.yaml"
)

// ErrMalformedSection is returned when the existing .gitignore
// contains exactly one of the two sentinels (interrupted edit, user
// hand-removal). Callers should surface this as a user-fixable error.
var ErrMalformedSection = errors.New("gitignore: malformed managed section (one sentinel missing)")

// Apply rewrites the managed section of <rootDir>/.gitignore so it
// contains exactly `paths` (sorted alphabetically). Lines outside the
// section are preserved verbatim. The operation is idempotent: if the
// final bytes equal the existing bytes, no write happens.
//
// Passing an empty `paths` removes the entire managed section
// (including its sentinels). If neither the file nor `paths` exist,
// Apply is a no-op.
func Apply(rootDir string, paths []string) error {
	path := filepath.Join(rootDir, gitignoreFilename)
	existing, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("gitignore: read %s: %w", path, readErr)
	}

	// Normalize CRLF→LF in memory so the byte-compare doesn't
	// false-positive on a hand-edited file.
	normalized := strings.ReplaceAll(string(existing), "\r\n", "\n")

	updated, err := rewrite(normalized, paths)
	if err != nil {
		return err
	}

	// Nothing to write if no file existed and no managed content to
	// emit.
	if os.IsNotExist(readErr) && updated == "" {
		return nil
	}

	// Skip the write when the file is byte-identical post-normalize.
	if !os.IsNotExist(readErr) && updated == normalized {
		return nil
	}

	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return fmt.Errorf("gitignore: mkdir %s: %w", rootDir, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("gitignore: write tmp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("gitignore: rename %s: %w", path, err)
	}
	return nil
}

// Read returns the managed paths inside the section and the foreign
// content outside it. Useful for tests and audits. foreign preserves
// the original byte arrangement minus the managed section.
func Read(rootDir string) (managed []string, foreign string, err error) {
	path := filepath.Join(rootDir, gitignoreFilename)
	body, readErr := os.ReadFile(path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return nil, "", nil
		}
		return nil, "", readErr
	}
	normalized := strings.ReplaceAll(string(body), "\r\n", "\n")
	managed, foreign, _, _, perr := splitSection(normalized)
	if perr != nil {
		return nil, "", perr
	}
	return managed, foreign, nil
}

// rewrite produces the new file content given the existing
// (normalized) content and the proposed managed paths.
func rewrite(existing string, paths []string) (string, error) {
	cleaned := uniqueSorted(paths)

	managedExisting, foreign, beforeBlank, afterBlank, err := splitSection(existing)
	if err != nil {
		return "", err
	}
	_ = managedExisting

	if len(cleaned) == 0 {
		// Strip the section entirely. Foreign content survives.
		return foreign, nil
	}

	// Build the managed block.
	var sb strings.Builder
	sb.WriteString(MarkerBegin)
	sb.WriteString("\n")
	for _, p := range cleaned {
		sb.WriteString(p)
		sb.WriteString("\n")
	}
	if needsFDHNegation(cleaned) {
		sb.WriteString(negationManifest)
		sb.WriteString("\n")
		sb.WriteString(negationLock)
		sb.WriteString("\n")
	}
	sb.WriteString(MarkerEnd)
	sb.WriteString("\n")
	block := sb.String()

	if foreign == "" {
		return block, nil
	}

	// Place the block where the old section was if there was one;
	// otherwise append after foreign content with one blank line of
	// separation if the foreign content doesn't already end with one.
	if hadSection := beforeBlank != "" || afterBlank != ""; hadSection {
		return beforeBlank + block + afterBlank, nil
	}
	sep := "\n"
	if !strings.HasSuffix(foreign, "\n") {
		sep = "\n\n"
	} else if !strings.HasSuffix(foreign, "\n\n") {
		sep = "\n"
	}
	return foreign + sep + block, nil
}

// splitSection finds the managed section in body and returns:
//   - managedLines: the path entries (sans sentinels and blank lines)
//   - foreign: body with the section (and the joining whitespace) stripped
//   - before, after: the foreign content before/after the section,
//     concatenated as `foreign`. Used by rewrite to place a new block
//     where the old one was.
//
// If the body has no section at all, managedLines is nil and foreign
// is body verbatim.
func splitSection(body string) (managedLines []string, foreign, before, after string, err error) {
	beginIdx := strings.Index(body, MarkerBegin)
	endIdx := strings.Index(body, MarkerEnd)

	if beginIdx == -1 && endIdx == -1 {
		return nil, body, "", "", nil
	}
	if beginIdx == -1 || endIdx == -1 || endIdx < beginIdx {
		return nil, "", "", "", ErrMalformedSection
	}

	// Expand the section bounds to include the line endings around it
	// so removing the section doesn't leave a stranded blank line.
	begin := beginIdx
	// Pull back to start of preceding blank line if any.
	if begin > 0 && body[begin-1] == '\n' {
		// keep the newline that terminated the preceding line —
		// removing it would join lines.
	}
	end := endIdx + len(MarkerEnd)
	if end < len(body) && body[end] == '\n' {
		end++ // include trailing newline of the closing sentinel
	}

	before = body[:begin]
	after = body[end:]

	// Trim one extra blank line that may have separated foreign
	// content from the section.
	if strings.HasSuffix(before, "\n\n") && (strings.HasPrefix(after, "") /* trivially true */) {
		// drop only ONE blank line on the leading side
		before = strings.TrimSuffix(before, "\n")
	}
	if strings.HasPrefix(after, "\n") {
		// We already absorbed the trailing newline of the END marker.
		// Keep any subsequent blank lines verbatim.
	}

	foreign = before + after

	// Extract lines between sentinels.
	inside := body[beginIdx+len(MarkerBegin) : endIdx]
	for _, line := range strings.Split(inside, "\n") {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		managedLines = append(managedLines, l)
	}
	return managedLines, foreign, before, after, nil
}

// uniqueSorted dedups and sorts paths alphabetically.
func uniqueSorted(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func needsFDHNegation(paths []string) bool {
	for _, p := range paths {
		if strings.HasPrefix(p, ".fdh/") {
			return true
		}
	}
	return false
}
