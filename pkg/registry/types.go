package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Index is the registry's top-level catalog (index.json at the registry root).
type Index struct {
	SchemaVersion int           `json:"schema_version"`
	Registry      string        `json:"registry"`
	Skills        []IndexEntry  `json:"skills"`
}

// IndexEntry is the per-skill summary stored in index.json.
type IndexEntry struct {
	Namespace      string   `json:"namespace"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	OwnerTeam      string   `json:"owner_team"`
	Tags           []string `json:"tags,omitempty"`
	LatestVersion  string   `json:"latest_version"`
	LatestHash     string   `json:"latest_hash"`
	ScanStatus     string   `json:"scan_status"` // "pass" | "warn" | "fail" | "none"
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
type Version struct {
	Version     string `json:"version"`     // semver
	ContentHash string `json:"content_hash"` // canonical sha256 hex
	PublishedAt string `json:"published_at"` // ISO 8601 UTC
	PublishedBy string `json:"published_by"`
	ChangelogURL string `json:"changelog_url,omitempty"`
	ScanStatus  string `json:"scan_status"` // "pass" | "warn" | "fail" | "none"
	Signature   string `json:"signature,omitempty"`
}

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
