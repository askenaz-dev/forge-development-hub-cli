// Package provenance handles the .skill-meta.yaml sidecar and the
// installed_from frontmatter breadcrumb defined in the skill-provenance spec.
package provenance

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is the version of the sidecar schema. Bumps follow the
// skill-provenance spec.
const SchemaVersion = 1

// SidecarFilename is the on-disk filename of the provenance sidecar.
const SidecarFilename = ".skill-meta.yaml"

// FrontmatterKey is the lone key the installer injects into SKILL.md
// frontmatter. The portability lint explicitly allows this key.
const FrontmatterKey = "installed_from"

// SkillMeta is the sidecar's data shape. Field order matches the YAML
// emission order; do NOT reorder without a schema-version bump.
type SkillMeta struct {
	SchemaVersion    int      `yaml:"schema_version"`
	Registry         string   `yaml:"registry"`
	Namespace        string   `yaml:"namespace"`
	Name             string   `yaml:"name"`
	Version          string   `yaml:"version"`
	ContentHash      string   `yaml:"content_hash"`
	InstalledBy      string   `yaml:"installed_by"`
	InstalledAt      string   `yaml:"installed_at"`
	TargetAgents     []string `yaml:"target_agents"`
	Scope            string   `yaml:"scope"`
	Path             string   `yaml:"path"`
	InstallerVersion string   `yaml:"installer_version"`
	Signature        string   `yaml:"signature,omitempty"`
}

// WriteSidecar serialises meta to <dir>/.skill-meta.yaml. The file is
// rewritten on each install (idempotent for the same content).
func WriteSidecar(dir string, meta SkillMeta) error {
	if meta.SchemaVersion == 0 {
		meta.SchemaVersion = SchemaVersion
	}
	if meta.InstalledAt == "" {
		meta.InstalledAt = time.Now().UTC().Format(time.RFC3339)
	}
	sort.Strings(meta.TargetAgents)

	buf, err := yaml.Marshal(&meta)
	if err != nil {
		return fmt.Errorf("marshal sidecar: %w", err)
	}
	header := []byte("# fdh sidecar; do not edit by hand.\n")
	payload := append(header, buf...)
	target := filepath.Join(dir, SidecarFilename)
	return os.WriteFile(target, payload, 0o644)
}

// ReadSidecar reads <dir>/.skill-meta.yaml. Returns (meta, nil) on success,
// (zero, nil) when the file does not exist, or (zero, err) on parse failures.
func ReadSidecar(dir string) (SkillMeta, error) {
	path := filepath.Join(dir, SidecarFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SkillMeta{}, nil
		}
		return SkillMeta{}, err
	}
	var meta SkillMeta
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return SkillMeta{}, fmt.Errorf("parse sidecar %s: %w", path, err)
	}
	if meta.SchemaVersion == 0 {
		return SkillMeta{}, fmt.Errorf("sidecar %s missing schema_version", path)
	}
	return meta, nil
}

// SidecarPresent reports whether dir contains a parseable sidecar.
func SidecarPresent(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, SidecarFilename))
	return err == nil
}

// InjectBreadcrumb returns a new []byte where the SKILL.md frontmatter
// has been updated to contain exactly one `installed_from: <ref>` line.
//
// The function is idempotent: re-running it with the same ref produces a
// byte-identical result; re-running with a new ref replaces the value but
// does not add a duplicate key. The rest of the document (including line
// endings, ordering of other keys, and the body) is preserved byte-identically.
//
// If the input has no frontmatter (no opening `---` on the first line),
// the function inserts a minimal frontmatter block at the very top
// containing only the breadcrumb.
func InjectBreadcrumb(src []byte, ref string) []byte {
	if ref == "" {
		return src
	}

	// Strip optional UTF-8 BOM at the start; we re-add it on the way out.
	var bom []byte
	if bytes.HasPrefix(src, []byte{0xEF, 0xBB, 0xBF}) {
		bom = src[:3]
		src = src[3:]
	}

	if !startsWithFrontmatter(src) {
		// Insert a fresh frontmatter block.
		// Use LF endings for the inserted block; existing body line endings
		// are preserved.
		fm := []byte("---\n" + FrontmatterKey + ": " + ref + "\n---\n")
		out := append(bom, fm...)
		out = append(out, src...)
		return out
	}

	// Locate the closing `---`. We scan line-by-line preserving original
	// line endings to keep the rest of the document byte-identical.
	closeStart, closeEnd, ok := findFrontmatterCloseRange(src)
	if !ok {
		// Malformed frontmatter (no closing ---). Leave the file unchanged
		// rather than risk corrupting it; the caller should have validated
		// the bundle earlier.
		out := append(bom, src...)
		return out
	}

	// Slice the frontmatter content (between the opening "---<nl>" and the
	// closing "---<nl>") and produce a new body that contains exactly one
	// installed_from line.
	openEnd := frontmatterOpenEnd(src) // index just past the opening "---<nl>"

	fmContent := src[openEnd:closeStart] // raw bytes of frontmatter lines

	updated := replaceOrAppendKey(fmContent, FrontmatterKey, ref)

	// Reassemble.
	var out bytes.Buffer
	out.Write(bom)
	out.Write(src[:openEnd])
	out.Write(updated)
	out.Write(src[closeStart:closeEnd]) // closing "---<nl>"
	out.Write(src[closeEnd:])           // body
	return out.Bytes()
}

// StripBreadcrumb returns src with any `installed_from:` line removed from
// frontmatter. Used by tests to verify byte-identity outside the breadcrumb.
func StripBreadcrumb(src []byte) []byte {
	if !startsWithFrontmatter(src) {
		return src
	}
	openEnd := frontmatterOpenEnd(src)
	closeStart, closeEnd, ok := findFrontmatterCloseRange(src)
	if !ok {
		return src
	}
	fmContent := src[openEnd:closeStart]

	var out bytes.Buffer
	lines := splitLinesPreserve(fmContent)
	for _, ln := range lines {
		if hasKeyPrefix(ln, FrontmatterKey) {
			continue
		}
		out.Write(ln)
	}

	stripped := out.Bytes()

	var final bytes.Buffer
	final.Write(src[:openEnd])
	final.Write(stripped)
	final.Write(src[closeStart:closeEnd])
	final.Write(src[closeEnd:])
	return final.Bytes()
}

// replaceOrAppendKey scans fmContent line-by-line. If a line begins with
// "<key>:" (after optional whitespace) it is replaced with the new value;
// otherwise a new line is appended at the end. Line endings are preserved
// for existing content; the appended line uses the dominant line ending in
// the input (LF if mixed, CRLF if any CRLF found and no LF without CR).
func replaceOrAppendKey(fmContent []byte, key, value string) []byte {
	lineEnding := dominantLineEnding(fmContent)
	lines := splitLinesPreserve(fmContent)

	replaced := false
	var out bytes.Buffer
	for _, ln := range lines {
		if !replaced && hasKeyPrefix(ln, key) {
			// Replace this line with the new key: value plus the original line ending.
			out.WriteString(key + ": " + value)
			out.WriteString(extractLineEnding(ln, lineEnding))
			replaced = true
			continue
		}
		out.Write(ln)
	}
	if !replaced {
		// Make sure we end with a newline before appending so the new line
		// doesn't glue onto the previous one.
		if out.Len() > 0 && !endsWithLineEnding(out.Bytes()) {
			out.WriteString(lineEnding)
		}
		out.WriteString(key + ": " + value + lineEnding)
	}
	return out.Bytes()
}

func dominantLineEnding(b []byte) string {
	if bytes.Contains(b, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

// splitLinesPreserve splits b into lines keeping each line's trailing
// newline (LF or CRLF) so the caller can preserve byte-identity.
func splitLinesPreserve(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			out = append(out, b[start:i+1])
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

func hasKeyPrefix(line []byte, key string) bool {
	trimmed := bytes.TrimLeft(line, " \t")
	if len(trimmed) < len(key)+1 {
		return false
	}
	if !bytes.HasPrefix(trimmed, []byte(key)) {
		return false
	}
	rest := trimmed[len(key):]
	if len(rest) == 0 {
		return false
	}
	return rest[0] == ':'
}

func extractLineEnding(line []byte, fallback string) string {
	if bytes.HasSuffix(line, []byte("\r\n")) {
		return "\r\n"
	}
	if bytes.HasSuffix(line, []byte("\n")) {
		return "\n"
	}
	return fallback
}

func endsWithLineEnding(b []byte) bool {
	return len(b) > 0 && b[len(b)-1] == '\n'
}

func startsWithFrontmatter(b []byte) bool {
	return bytes.HasPrefix(b, []byte("---\n")) || bytes.HasPrefix(b, []byte("---\r\n"))
}

// frontmatterOpenEnd returns the byte index just past the opening "---\n"
// (or "---\r\n") delimiter.
func frontmatterOpenEnd(b []byte) int {
	if bytes.HasPrefix(b, []byte("---\r\n")) {
		return 5
	}
	return 4 // "---\n"
}

// findFrontmatterCloseRange returns [start, end) covering the closing
// "---\n" (or "---\r\n") delimiter line in b. start is the offset of the
// first byte of the closing line; end is the offset just past its line
// terminator. ok is false if no closing delimiter exists.
func findFrontmatterCloseRange(b []byte) (int, int, bool) {
	openEnd := frontmatterOpenEnd(b)
	pos := openEnd
	for pos < len(b) {
		// Find next newline.
		nl := bytes.IndexByte(b[pos:], '\n')
		if nl < 0 {
			return 0, 0, false
		}
		lineStart := pos
		lineEnd := pos + nl + 1
		line := b[lineStart:lineEnd]
		// Strip the terminator(s) for the equality check, but report
		// the range that includes them.
		stripped := bytes.TrimRight(line, "\r\n")
		if string(stripped) == "---" {
			return lineStart, lineEnd, true
		}
		pos = lineEnd
	}
	return 0, 0, false
}

// MakeBreadcrumbRef constructs the canonical `installed_from` value:
//
//	<registry>/<namespace>/<name>@<version>
//
// where registry is a base URL or path and the others are exactly as
// recorded in the registry's index/manifest.
func MakeBreadcrumbRef(registry, namespace, name, version string) string {
	reg := strings.TrimRight(registry, "/")
	return fmt.Sprintf("%s/%s/%s@%s", reg, namespace, name, version)
}
