// Package managed defines the `.fdh-managed.yaml` ownership marker
// that `fdh install` writes next to every component it materializes,
// plus a back-compat reader for the legacy `.skill-version` markers
// shipped by earlier CLI releases.
//
// The marker is the source of truth for "fdh owns this path".
// Consumer code (gitignore manager, update planner, doctor drift
// detection, uninstall) consults it to decide whether a target is
// touchable. Its absence means "developer-owned, do not touch."
//
// Boundaries: this package depends only on stdlib + yaml.v3. It MUST
// NOT import pkg/adapters or pkg/gitignore.
package managed

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Filename is the canonical marker filename for directory-based
// adapters (claude-code, codex). Flat adapters get a sibling marker
// instead — see FilenameFor().
const Filename = ".fdh-managed.yaml"

// Kind constants shared with pkg/hubregistry (kept independent here
// to preserve the boundary).
const (
	KindSkill = "skill"
	KindRule  = "rule"
	KindAgent = "agent"
	KindHook  = "hook"
)

// legacyDirMarker / legacyFlatPrefix match the deprecated marker
// names shipped by earlier CLI releases. Kept for migration.
const (
	legacyDirMarker  = ".skill-version"
	legacyFlatPrefix = ".skill-version-"
	legacyFlatSuffix = "" // historical: just legacyFlatPrefix + <name>
	newFlatSuffix    = ".fdh-managed.yaml"
)

// Marker is the on-disk YAML shape of `.fdh-managed.yaml`.
//
// Field order in struct declaration MATTERS for serialization
// stability — keep it aligned with the on-disk order callers expect.
type Marker struct {
	// Name is the component's identifier (kebab-case).
	Name string `yaml:"name"`

	// Kind is one of: skill, rule, agent, hook. Required by the spec;
	// the migrator defaults to "skill" when reading legacy markers.
	Kind string `yaml:"kind"`

	// Version is the component's catalog version at install time.
	Version string `yaml:"version,omitempty"`

	// HubCommit is the SHA of the hub HEAD at install time.
	HubCommit string `yaml:"hub_commit,omitempty"`

	// InstalledAt is the UTC timestamp the marker was first written.
	InstalledAt time.Time `yaml:"installed_at"`

	// InstalledByFDH is the semver of the CLI that wrote the marker.
	InstalledByFDH string `yaml:"installed_by_fdh,omitempty"`

	// SourcePath is the component's directory inside the hub
	// (e.g. "skills/design-system", "rules/no-console-log").
	SourcePath string `yaml:"source_path,omitempty"`

	// ContentHash is the canonical SHA-256 of the installed content
	// at install time. Drift detection recomputes and compares.
	ContentHash string `yaml:"content_hash,omitempty"`

	// Agent identifies which adapter wrote the marker — useful when
	// several agents share a directory tree.
	Agent string `yaml:"agent,omitempty"`
}

// FilenameFor returns the marker filename for a target.
//
// For directory-based installs (claude-code, codex) the marker lives
// inside the directory as a fixed Filename ('.fdh-managed.yaml').
// For flat installs (copilot, opencode) the marker is a sibling of
// the materialized file named '<basename>.fdh-managed.yaml'.
func FilenameFor(basename string, isFlat bool) string {
	if !isFlat {
		return Filename
	}
	return basename + newFlatSuffix
}

// IsManagedFilename reports whether name is a canonical
// `.fdh-managed.yaml` marker (including the flat-sibling form).
func IsManagedFilename(name string) bool {
	if name == Filename {
		return true
	}
	return strings.HasSuffix(name, newFlatSuffix) && name != newFlatSuffix
}

// IsLegacyFilename reports whether name is a legacy `.skill-version`
// marker (including the flat per-skill suffix).
func IsLegacyFilename(name string) bool {
	if name == legacyDirMarker {
		return true
	}
	return strings.HasPrefix(name, legacyFlatPrefix) && name != legacyFlatPrefix
}

// IsAnyMarkerFilename reports whether name is a current or legacy
// marker filename. Useful for walkers that exclude markers from
// content-hash computation.
func IsAnyMarkerFilename(name string) bool {
	return IsManagedFilename(name) || IsLegacyFilename(name)
}

// Write serializes m to a marker file:
//   - For directory-based installs (isFlat=false) writes
//     <dir>/.fdh-managed.yaml; basename is ignored.
//   - For flat installs (isFlat=true) writes
//     <dir>/<basename>.fdh-managed.yaml.
//
// Returns the absolute path of the written file. Auto-fills
// InstalledAt with time.Now().UTC() when zero.
func Write(dir, basename string, m Marker, isFlat bool) (string, error) {
	if dir == "" {
		return "", errors.New("managed.Write: dir is empty")
	}
	if m.InstalledAt.IsZero() {
		m.InstalledAt = time.Now().UTC()
	}
	body, err := yaml.Marshal(&m)
	if err != nil {
		return "", fmt.Errorf("managed.Write: marshal: %w", err)
	}
	name := FilenameFor(basename, isFlat)
	if name == "" {
		return "", errors.New("managed.Write: empty filename")
	}
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("managed.Write: mkdir %s: %w", dir, err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", fmt.Errorf("managed.Write: write %s: %w", path, err)
	}
	return path, nil
}

// Read parses a marker file at path. Tolerates legacy markers — if
// the file is a `.skill-version` it is decoded with the legacy shape
// and converted (without writing) to the new Marker form.
func Read(path string) (Marker, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Marker{}, err
	}
	return decodeMarker(body, filepath.Base(path))
}

// decodeMarker decodes body as either a Marker or a legacy
// SkillVersionMarker shape. filename is used to disambiguate when
// fields are sparse and to derive Name for legacy flat markers.
func decodeMarker(body []byte, filename string) (Marker, error) {
	// New shape first.
	if IsManagedFilename(filename) {
		var m Marker
		if err := yaml.Unmarshal(body, &m); err != nil {
			return Marker{}, fmt.Errorf("managed.Read: unmarshal %s: %w", filename, err)
		}
		return m, nil
	}
	// Legacy shape: same fields minus Kind and SourcePath; HubVersion
	// in legacy corresponds to Version in the new shape.
	legacy := struct {
		Name           string    `yaml:"name"`
		HubVersion     string    `yaml:"hub_version"`
		HubCommit      string    `yaml:"hub_commit"`
		InstalledAt    time.Time `yaml:"installed_at"`
		InstalledByFDH string    `yaml:"installed_by_fdh"`
		ContentHash    string    `yaml:"content_hash"`
		Agent          string    `yaml:"agent"`
	}{}
	if err := yaml.Unmarshal(body, &legacy); err != nil {
		return Marker{}, fmt.Errorf("managed.Read: unmarshal legacy %s: %w", filename, err)
	}
	name := legacy.Name
	if name == "" && strings.HasPrefix(filename, legacyFlatPrefix) {
		name = strings.TrimPrefix(filename, legacyFlatPrefix)
	}
	return Marker{
		Name:           name,
		Kind:           "skill", // legacy is skill-only
		Version:        legacy.HubVersion,
		HubCommit:      legacy.HubCommit,
		InstalledAt:    legacy.InstalledAt,
		InstalledByFDH: legacy.InstalledByFDH,
		SourcePath:     legacySourcePath(name),
		ContentHash:    legacy.ContentHash,
		Agent:          legacy.Agent,
	}, nil
}

func legacySourcePath(name string) string {
	if name == "" {
		return ""
	}
	return "skills/" + name
}

// Migrate converts a legacy `.skill-version` marker at legacyPath to
// the canonical `.fdh-managed.yaml` form in the same location. The
// new file is written first; on success the legacy file is removed.
// If both files already exist, the new one wins and the legacy is
// removed without re-writing the new one.
//
// Returns the absolute path of the canonical marker plus the parsed
// Marker.
func Migrate(legacyPath string) (string, Marker, error) {
	legacyName := filepath.Base(legacyPath)
	if !IsLegacyFilename(legacyName) {
		return "", Marker{}, fmt.Errorf("managed.Migrate: %s is not a legacy marker filename", legacyName)
	}
	dir := filepath.Dir(legacyPath)

	// Compute target name.
	var targetPath string
	if legacyName == legacyDirMarker {
		targetPath = filepath.Join(dir, Filename)
	} else {
		// Flat: .skill-version-<name> → <basename>.fdh-managed.yaml
		// where <basename> is the materialized file name. We can only
		// recover it from the legacy name + the conventional adapter
		// naming, but the safer path is to keep the same suffix:
		// turn `.skill-version-<name>` into `<name>.fdh-managed.yaml`.
		name := strings.TrimPrefix(legacyName, legacyFlatPrefix)
		targetPath = filepath.Join(dir, name+newFlatSuffix)
	}

	// If the canonical marker already exists, prefer it and just
	// remove the legacy.
	if existing, err := os.ReadFile(targetPath); err == nil {
		m, derr := decodeMarker(existing, filepath.Base(targetPath))
		_ = os.Remove(legacyPath)
		if derr != nil {
			return targetPath, Marker{}, derr
		}
		return targetPath, m, nil
	}

	// Read the legacy.
	body, err := os.ReadFile(legacyPath)
	if err != nil {
		return "", Marker{}, err
	}
	m, err := decodeMarker(body, legacyName)
	if err != nil {
		return "", Marker{}, err
	}
	out, err := yaml.Marshal(&m)
	if err != nil {
		return "", Marker{}, fmt.Errorf("managed.Migrate: marshal: %w", err)
	}
	if err := os.WriteFile(targetPath, out, 0o644); err != nil {
		return "", Marker{}, fmt.Errorf("managed.Migrate: write %s: %w", targetPath, err)
	}
	if err := os.Remove(legacyPath); err != nil {
		// Best-effort; the new file is already in place.
		return targetPath, m, fmt.Errorf("managed.Migrate: remove legacy %s: %w", legacyPath, err)
	}
	return targetPath, m, nil
}
