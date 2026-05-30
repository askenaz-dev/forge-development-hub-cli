package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Index is the registry's top-level catalog (index.json at the registry root).
//
// Two shapes are accepted on the wire:
//   - v2 (current): `components[]`, every entry carrying an explicit `kind`.
//   - v1 (legacy):  `skills[]`, no `kind` (implicitly "skill").
//
// Both JSON fields are declared so strict decoding accepts either document.
// normalize() reconciles them so callers can rely on Components (all kinds)
// and Skills (the kind=="skill" view) regardless of the source version.
type Index struct {
	SchemaVersion int          `json:"schema_version"`
	Registry      string       `json:"registry"`
	Components    []IndexEntry `json:"components,omitempty"`
	Skills        []IndexEntry `json:"skills,omitempty"`
}

// IndexEntry is the per-component summary stored in index.json.
type IndexEntry struct {
	Kind          string   `json:"kind,omitempty"` // skill|rule|agent|hook; "" in v1 docs ⇒ skill
	Namespace     string   `json:"namespace"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	OwnerTeam     string   `json:"owner_team"`
	Tags          []string `json:"tags,omitempty"`
	LatestVersion string   `json:"latest_version"`
	LatestHash    string   `json:"latest_hash"`
	ScanStatus    string   `json:"scan_status"` // "pass" | "warn" | "fail" | "none"
}

// normalize reconciles the v1 (skills[]) and v2 (components[]) shapes. After
// it runs, Components holds every entry (each with a non-empty Kind) and
// Skills holds the kind=="skill" subset. Idempotent.
func (idx *Index) normalize() {
	if len(idx.Components) == 0 && len(idx.Skills) > 0 {
		// v1 document: promote skills to components with kind=skill.
		idx.Components = make([]IndexEntry, 0, len(idx.Skills))
		for _, e := range idx.Skills {
			if e.Kind == "" {
				e.Kind = "skill"
			}
			idx.Components = append(idx.Components, e)
		}
	} else {
		// v2 document (or empty): default any blank kind and derive Skills.
		for i := range idx.Components {
			if idx.Components[i].Kind == "" {
				idx.Components[i].Kind = "skill"
			}
		}
	}
	skills := make([]IndexEntry, 0, len(idx.Components))
	for _, e := range idx.Components {
		if e.Kind == "skill" {
			skills = append(skills, e)
		}
	}
	idx.Skills = skills
}

// ComponentsByKind returns the catalog entries of the given kind.
func (idx Index) ComponentsByKind(kind string) []IndexEntry {
	out := make([]IndexEntry, 0, len(idx.Components))
	for _, e := range idx.Components {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// toSummary projects an IndexEntry onto the (kind-less) SkillSummary used by
// Search. Explicit because adding Kind to IndexEntry broke the previous
// direct struct conversion.
func (e IndexEntry) toSummary() SkillSummary {
	return SkillSummary{
		Namespace:     e.Namespace,
		Name:          e.Name,
		Description:   e.Description,
		OwnerTeam:     e.OwnerTeam,
		Tags:          e.Tags,
		LatestVersion: e.LatestVersion,
		LatestHash:    e.LatestHash,
		ScanStatus:    e.ScanStatus,
	}
}

// Manifest is the per-skill manifest.json file inside skills/<ns>/<name>/.
type Manifest struct {
	SchemaVersion int       `json:"schema_version"`
	Namespace     string    `json:"namespace"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	OwnerTeam     string    `json:"owner_team"`
	Tags          []string  `json:"tags,omitempty"`
	Latest        string    `json:"latest"`
	Versions      []Version `json:"versions"`
}

// Version is one entry under Manifest.Versions.
//
// The Status field implements capability `component-lifecycle`:
//   - "active"     — installable and selectable by default (zero value).
//   - "deprecated" — still installable, but resolution emits a warning
//     and `fdh doctor` flags consumers pinned to this
//     version. Forward-only: active → deprecated.
//   - "yanked"     — excluded from constraint resolution; install
//     refuses without `--allow-yanked <version>`. Wire
//     protocol serves 410 Gone for the bundle.
//     Forward-only: deprecated → yanked, no un-yank.
type Version struct {
	Version      string `json:"version"`      // semver
	ContentHash  string `json:"content_hash"` // canonical sha256 hex
	PublishedAt  string `json:"published_at"` // ISO 8601 UTC
	PublishedBy  string `json:"published_by"`
	ChangelogURL string `json:"changelog_url,omitempty"`
	ScanStatus   string `json:"scan_status"` // "pass" | "warn" | "fail" | "none"
	Signature    string `json:"signature,omitempty"`
	Status       string `json:"status,omitempty"` // "active" | "deprecated" | "yanked"
}

// Lifecycle constants for Version.Status.
const (
	StatusActive     = "active"
	StatusDeprecated = "deprecated"
	StatusYanked     = "yanked"
)

// IsYanked reports whether the version is yanked.
func (v Version) IsYanked() bool { return v.Status == StatusYanked }

// IsDeprecated reports whether the version is deprecated.
func (v Version) IsDeprecated() bool { return v.Status == StatusDeprecated }

// SkillSummary is the row produced by Search.
type SkillSummary struct {
	Namespace     string
	Name          string
	Description   string
	OwnerTeam     string
	Tags          []string
	LatestVersion string
	LatestHash    string
	ScanStatus    string
}

// FindVersion returns the Version entry matching v, or nil.
func (m *Manifest) FindVersion(v string) *Version {
	for i := range m.Versions {
		if m.Versions[i].Version == v {
			return &m.Versions[i]
		}
	}
	return nil
}

// unmarshalStrict decodes JSON with unknown-field rejection.
func unmarshalStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("strict decode: %w", err)
	}
	// Ensure no trailing junk.
	if dec.More() {
		return fmt.Errorf("strict decode: trailing data")
	}
	return nil
}
