package adapters

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/forge/fdh/pkg/managed"
)

// MarkerName returns the marker filename for a (agent, skillName)
// pair using the legacy `.skill-version` convention.
//
// Deprecated: use managed.FilenameFor (with isFlat selected by
// agent kind). Kept for back-compat with external callers during the
// transition window.
func MarkerName(agent, skillName string) string {
	switch agent {
	case "claude-code", "codex":
		return managed.Filename
	case "copilot":
		return managed.FilenameFor(skillName+".prompt.md", true)
	case "opencode":
		return managed.FilenameFor(skillName+".md", true)
	default:
		return managed.FilenameFor(skillName+".md", true)
	}
}

// MarshalMarker serializes a SkillVersionMarker to its on-disk YAML
// form.
//
// Deprecated: use managed.Write directly. Kept for tests during the
// transition window.
func MarshalMarker(m SkillVersionMarker) ([]byte, error) {
	if m.InstalledAt.IsZero() {
		m.InstalledAt = time.Now().UTC()
	}
	body, err := yaml.Marshal(&m)
	if err != nil {
		return nil, fmt.Errorf("marshal marker: %w", err)
	}
	return body, nil
}

// UnmarshalMarker reads a marker file body.
//
// Deprecated: use managed.Read directly.
func UnmarshalMarker(body []byte) (SkillVersionMarker, error) {
	var m SkillVersionMarker
	if err := yaml.Unmarshal(body, &m); err != nil {
		return SkillVersionMarker{}, fmt.Errorf("unmarshal marker: %w", err)
	}
	return m, nil
}

// LoadMarker reads and parses the marker at path. Tolerates legacy
// `.skill-version` markers by routing through managed.Read.
func LoadMarker(path string) (SkillVersionMarker, error) {
	m, err := managed.Read(path)
	if err != nil {
		return SkillVersionMarker{}, err
	}
	return SkillVersionMarker(m), nil
}

// IsMarkerFilename reports whether name matches a current or legacy
// marker filename convention.
func IsMarkerFilename(name string) bool {
	if managed.IsManagedFilename(name) {
		return true
	}
	if managed.IsLegacyFilename(name) {
		return true
	}
	// Defensive: an early implementation of flat markers used the
	// raw `.skill-version-<name>` form, which IsLegacyFilename
	// already covers; keep an explicit guard for the empty-suffix
	// edge case that strings.HasPrefix matches.
	return strings.HasPrefix(name, ".skill-version-") && name != ".skill-version-"
}

// IsMarkerFilenameAny is the same predicate as IsMarkerFilename;
// kept as a non-Deprecated alias so external code can adopt the
// clearer name when it migrates off the legacy.
func IsMarkerFilenameAny(name string) bool { return IsMarkerFilename(name) }

// MigrateLegacyMarker rewrites a legacy `.skill-version` file at path as
// `.fdh-managed.yaml` in the same directory. No-op when path is already
// canonical.
func MigrateLegacyMarker(path string) (string, SkillVersionMarker, error) {
	base := pathBase(path)
	if managed.IsLegacyFilename(base) {
		newPath, m, err := managed.Migrate(path)
		return newPath, SkillVersionMarker(m), err
	}
	// Path is already canonical; return as-is.
	m, err := managed.Read(path)
	if err != nil {
		return path, SkillVersionMarker{}, err
	}
	return path, SkillVersionMarker(m), nil
}

// pathBase isolates the import of path/filepath to one helper so
// downstream files don't accidentally take a transitive dep.
func pathBase(p string) string {
	// Use os.PathSeparator-aware split via stdlib.
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
