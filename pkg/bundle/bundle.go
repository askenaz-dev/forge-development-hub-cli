// Package bundle models a skill bundle on disk and exposes the canonical
// content-hash algorithm.
//
// A bundle is a directory containing:
//   - SKILL.md (required) with YAML frontmatter and markdown body
//   - scripts/, references/, assets/ (each optional)
//   - any additional files
//
// The bundle's canonical hash is computed over a normalized tree manifest
// (see design.md §"Canonical bundle hash") so the digest is deterministic
// across macOS, Linux, and Windows.
package bundle

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// NameRegex is the regular expression every skill name MUST match.
// See specs/skill-bundle-and-registry/spec.md "SKILL.md frontmatter validation".
var NameRegex = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Limits enforced by validation.
const (
	NameMinLen        = 1
	NameMaxLen        = 64
	DescriptionMinLen = 1
	DescriptionMaxLen = 1024
)

// OptionalSubdirs are the optional, well-known subdirectories defined by the
// open Agent Skills specification.
var OptionalSubdirs = []string{"scripts", "references", "assets"}

// Bundle represents a skill bundle laid out on disk. It is constructed via
// Load and carries enough state for validation, lint, and hashing.
type Bundle struct {
	// Root is the absolute path of the bundle directory (the parent of SKILL.md).
	Root string

	// DirName is the leaf directory name (filepath.Base(Root)). It MUST equal
	// the frontmatter name field per the bundle spec.
	DirName string

	// SkillMD holds the parsed SKILL.md document.
	SkillMD SkillMDDoc

	// Files lists every file in the bundle relative to Root, using forward
	// slashes regardless of OS. Used by Hash to produce a deterministic digest.
	Files []FileEntry
}

// FileEntry describes one file in a bundle, captured in a form that hashes
// identically across operating systems.
type FileEntry struct {
	// RelPath is the path relative to the bundle root, using forward slashes.
	RelPath string

	// Mode is the canonical mode: 100644 for regular files, 100755 for
	// regular files with the Unix executable bit set. Other mode bits
	// (sticky, setuid, etc.) are not represented in the canonical hash.
	Mode string

	// ContentSHA256 is the lowercase hex SHA-256 of the file's bytes.
	ContentSHA256 string
}

// Load reads a bundle from disk and returns a fully-populated Bundle. The
// caller is responsible for invoking Validate to enforce the requirement set.
func Load(root string) (*Bundle, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve bundle root: %w", err)
	}

	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("stat bundle root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("bundle root is not a directory: %s", absRoot)
	}

	skillPath := filepath.Join(absRoot, "SKILL.md")
	skillBytes, err := os.ReadFile(skillPath)
	if err != nil {
		return nil, fmt.Errorf("read SKILL.md: %w", err)
	}

	doc, err := ParseSkillMD(skillBytes)
	if err != nil {
		return nil, fmt.Errorf("parse SKILL.md: %w", err)
	}

	files, err := walkFiles(absRoot)
	if err != nil {
		return nil, fmt.Errorf("walk bundle: %w", err)
	}

	return &Bundle{
		Root:    absRoot,
		DirName: filepath.Base(absRoot),
		SkillMD: doc,
		Files:   files,
	}, nil
}

// walkFiles enumerates every regular file in root and returns them with
// canonical-mode + content hash, ready for Bundle.Hash.
func walkFiles(root string) ([]FileEntry, error) {
	var entries []FileEntry

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Hide files whose names start with "." (e.g., .skill-meta.yaml).
		// The sidecar is written next to an installed bundle, not part of
		// the source bundle. Excluding it here keeps Hash idempotent if
		// someone re-hashes an installed directory.
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// Canonical: forward slashes regardless of OS.
		relForward := filepath.ToSlash(rel)

		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := canonicalMode(info.Mode())

		sum, err := sha256File(path)
		if err != nil {
			return err
		}

		entries = append(entries, FileEntry{
			RelPath:       relForward,
			Mode:          mode,
			ContentSHA256: sum,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Lexicographic sort for hash determinism.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].RelPath < entries[j].RelPath
	})
	return entries, nil
}

// canonicalMode collapses a file mode into the two values the canonical
// hash recognizes: 100755 if any executable bit is set, 100644 otherwise.
// Note: on Windows the executable bit is not meaningful; this returns 100644
// in that case. Registries that publish bundles record the canonical mode
// in manifest.json so the install side knows what the *source* mode was,
// independent of the OS reading the bundle.
func canonicalMode(m fs.FileMode) string {
	if m&0o111 != 0 {
		return "100755"
	}
	return "100644"
}

// Validate enforces the structural rules from the bundle spec. It returns a
// nil error when every rule passes and a *ValidationError otherwise.
func (b *Bundle) Validate() error {
	var problems []string

	// SKILL.md is required (Load already verified it exists; double-check
	// is cheap and protects future callers that bypass Load).
	if _, err := os.Stat(filepath.Join(b.Root, "SKILL.md")); err != nil {
		problems = append(problems, "SKILL.md is missing or unreadable")
	}

	// Frontmatter rules.
	if b.SkillMD.Name == "" {
		problems = append(problems, "frontmatter: name is required")
	} else {
		if !NameRegex.MatchString(b.SkillMD.Name) {
			problems = append(problems, fmt.Sprintf(
				"frontmatter: name %q does not match %s",
				b.SkillMD.Name, NameRegex.String()))
		}
		if len(b.SkillMD.Name) < NameMinLen || len(b.SkillMD.Name) > NameMaxLen {
			problems = append(problems, fmt.Sprintf(
				"frontmatter: name length %d outside %d..%d",
				len(b.SkillMD.Name), NameMinLen, NameMaxLen))
		}
	}

	if b.SkillMD.Description == "" {
		problems = append(problems, "frontmatter: description is required")
	} else if dl := len(b.SkillMD.Description); dl < DescriptionMinLen || dl > DescriptionMaxLen {
		problems = append(problems, fmt.Sprintf(
			"frontmatter: description length %d outside %d..%d",
			dl, DescriptionMinLen, DescriptionMaxLen))
	}

	// name must match bundle directory name.
	if b.SkillMD.Name != "" && b.DirName != b.SkillMD.Name {
		problems = append(problems, fmt.Sprintf(
			"frontmatter name %q does not match bundle directory %q",
			b.SkillMD.Name, b.DirName))
	}

	// Optional subdirectories: if a path with the well-known name exists,
	// it MUST be a directory. A file named "scripts" (rather than a
	// directory) is rejected.
	for _, sub := range OptionalSubdirs {
		p := filepath.Join(b.Root, sub)
		info, err := os.Stat(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue // optional — absent is fine
			}
			problems = append(problems, fmt.Sprintf("stat %s/: %v", sub, err))
			continue
		}
		if !info.IsDir() {
			problems = append(problems, fmt.Sprintf("%q exists but is not a directory", sub))
		}
	}

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

// ValidationError aggregates structural problems found by Validate.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return "bundle validation: " + e.Problems[0]
	}
	return fmt.Sprintf("bundle validation: %d problems:\n  - %s",
		len(e.Problems), strings.Join(e.Problems, "\n  - "))
}
