package bundle

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// SkillMDDoc is a parsed SKILL.md document.
//
// Frontmatter is exposed as a strongly-typed view (the spec-portable fields)
// plus a raw map of every key seen. The raw map is what the portability lint
// inspects to enforce the portable allowlist; the typed fields are convenience
// accessors for everything else.
type SkillMDDoc struct {
	// Spec-portable typed fields.
	Name          string
	Description   string
	License       string
	Compatibility []string
	Portable      *bool // pointer so we can distinguish "unset" from "false"
	InstalledFrom string

	// Metadata is the open key-value bag the spec permits.
	Metadata map[string]any

	// Raw is every key that appeared in the frontmatter, useful for the
	// portability lint. Includes the strongly-typed keys above.
	Raw map[string]any

	// Body is the markdown content after the closing --- of the frontmatter.
	Body []byte

	// HasFrontmatter is true if the document opened with "---".
	HasFrontmatter bool
}

// ParseSkillMD parses a SKILL.md byte slice. It tolerates files without
// frontmatter (HasFrontmatter=false), Unix or Windows line endings, and a
// BOM at the start of the file. It does NOT enforce any requirement —
// callers do that via Bundle.Validate.
func ParseSkillMD(raw []byte) (SkillMDDoc, error) {
	// Strip a leading UTF-8 BOM if present.
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})

	doc := SkillMDDoc{
		Metadata: map[string]any{},
		Raw:      map[string]any{},
	}

	if !startsWithFrontmatterDelimiter(raw) {
		// No frontmatter; treat the entire file as body.
		doc.Body = raw
		return doc, nil
	}
	doc.HasFrontmatter = true

	// Split into frontmatter block and body. The opening "---" is on the
	// first line; the closing "---" is the next line consisting of only
	// "---" (with optional trailing CR).
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 1024*1024), 64*1024*1024)

	var fmLines []string
	var bodyBuf bytes.Buffer

	state := 0 // 0: before opening ---, 1: inside FM, 2: after closing ---
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimRight(line, "\r")

		switch state {
		case 0:
			if trimmed == "---" {
				state = 1
				continue
			}
			// Should not happen because we checked startsWithFrontmatterDelimiter.
			bodyBuf.WriteString(line)
			bodyBuf.WriteByte('\n')
		case 1:
			if trimmed == "---" {
				state = 2
				continue
			}
			fmLines = append(fmLines, line)
		case 2:
			bodyBuf.WriteString(line)
			bodyBuf.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return doc, fmt.Errorf("scan SKILL.md: %w", err)
	}

	// Preserve trailing newline behavior: if the original ended without one,
	// strip the one we added.
	body := bodyBuf.Bytes()
	if len(raw) > 0 && raw[len(raw)-1] != '\n' && len(body) > 0 && body[len(body)-1] == '\n' {
		body = body[:len(body)-1]
	}
	doc.Body = body

	// Decode frontmatter YAML into the raw map.
	fmJoined := strings.Join(fmLines, "\n")
	if strings.TrimSpace(fmJoined) != "" {
		if err := yaml.Unmarshal([]byte(fmJoined), &doc.Raw); err != nil {
			return doc, fmt.Errorf("parse frontmatter YAML: %w", err)
		}
	}

	// Populate typed fields from the raw map.
	if v, ok := doc.Raw["name"].(string); ok {
		doc.Name = v
	}
	if v, ok := doc.Raw["description"].(string); ok {
		doc.Description = v
	}
	if v, ok := doc.Raw["license"].(string); ok {
		doc.License = v
	}
	if v, ok := doc.Raw["installed_from"].(string); ok {
		doc.InstalledFrom = v
	}
	if v, ok := doc.Raw["portable"].(bool); ok {
		b := v
		doc.Portable = &b
	}
	if v, ok := doc.Raw["compatibility"]; ok {
		switch vv := v.(type) {
		case []any:
			for _, item := range vv {
				if s, ok := item.(string); ok {
					doc.Compatibility = append(doc.Compatibility, s)
				}
			}
		case []string:
			doc.Compatibility = append(doc.Compatibility, vv...)
		}
	}
	if v, ok := doc.Raw["metadata"].(map[string]any); ok {
		doc.Metadata = v
	}

	return doc, nil
}

// IsPortable reports whether the skill is portable. The default when the
// frontmatter omits the field is true (per the skill-portability spec).
func (d SkillMDDoc) IsPortable() bool {
	if d.Portable == nil {
		return true
	}
	return *d.Portable
}

func startsWithFrontmatterDelimiter(raw []byte) bool {
	// Accept "---" followed by either "\n" or "\r\n".
	if bytes.HasPrefix(raw, []byte("---\n")) {
		return true
	}
	if bytes.HasPrefix(raw, []byte("---\r\n")) {
		return true
	}
	// A file that is just "---" without a newline isn't a valid frontmatter
	// document; treat as no frontmatter.
	return false
}
