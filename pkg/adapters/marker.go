package adapters

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// MarkerName returns the conventional `.skill-version` filename for
// a given agent. Directory-based agents (Claude, Codex) use the
// fixed name `.skill-version` inside the skill folder. Flat agents
// (Copilot, OpenCode) co-locate one marker per skill next to the
// prompt file as `.skill-version-<name>`.
func MarkerName(agent, skillName string) string {
	switch agent {
	case "claude-code", "codex":
		return ".skill-version"
	case "copilot", "opencode":
		return ".skill-version-" + skillName
	default:
		// Defensive default: a name that's still parseable as a
		// marker by isMarker() so re-reads don't confuse the hash.
		return ".skill-version-" + skillName
	}
}

// MarshalMarker serializes a SkillVersionMarker to its on-disk YAML
// form. The marker is intentionally tiny so devs can read it.
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

// UnmarshalMarker reads a marker file body. Used by `fdh update`
// when discovering installed skills.
func UnmarshalMarker(body []byte) (SkillVersionMarker, error) {
	var m SkillVersionMarker
	if err := yaml.Unmarshal(body, &m); err != nil {
		return SkillVersionMarker{}, fmt.Errorf("unmarshal marker: %w", err)
	}
	return m, nil
}

// LoadMarker reads and parses the marker at path.
func LoadMarker(path string) (SkillVersionMarker, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return SkillVersionMarker{}, err
	}
	return UnmarshalMarker(body)
}

// IsMarkerFilename reports whether name matches the marker
// filename convention (exposed for callers walking install dirs).
func IsMarkerFilename(name string) bool {
	return name == ".skill-version" || strings.HasPrefix(name, ".skill-version-")
}
