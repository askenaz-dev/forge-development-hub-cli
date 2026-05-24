// Package hubregistry parses and serves the catalog at
// `skills/registry.yaml` inside a forge Development Hub clone.
//
// The hub is a Git repository whose `skills/` tree holds one
// directory per skill plus a top-level `registry.yaml` index. This
// package clones (or pulls) the hub into a per-user cache, decodes
// the YAML, validates it, and exposes the entries the rest of the
// CLI consumes (`fdh init` wizard, `fdh update`, `fdh
// validate-registry`).
//
// Boundaries: this package depends only on stdlib + go-git + yaml.v3
// + cyphar/filepath-securejoin. It does NOT import anything from
// `internal/cli` nor from `pkg/adapters`; the consumer wires the
// pieces together.
package hubregistry

import "time"

// SchemaVersion is the catalog schema version this package understands.
// The hub spec `hub-skills-registry` reserves higher numbers for
// breaking changes; values outside the set below are rejected by
// Validate.
const SchemaVersion = 1

// Registry is the in-memory shape of `skills/registry.yaml` plus
// runtime metadata (where it was loaded from, which commit).
type Registry struct {
	// SchemaVersion is the value declared at the top of registry.yaml.
	SchemaVersion int `yaml:"schema_version"`

	// GeneratedAt is the ISO-8601 timestamp the hub recorded when it
	// regenerated the catalog. Optional — empty if not present.
	GeneratedAt time.Time `yaml:"generated_at,omitempty"`

	// Skills is the catalog itself.
	Skills []SkillEntry `yaml:"skills"`

	// LocalPath is the absolute on-disk path of the hub clone the
	// Registry was loaded from. Populated by Load. Not serialised.
	LocalPath string `yaml:"-"`

	// HubCommit is the SHA of HEAD at load time. Populated by Load.
	// Recorded in `.skill-version` markers so `fdh update` can
	// compare against fresh state.
	HubCommit string `yaml:"-"`
}

// SkillEntry is one entry in the catalog.
type SkillEntry struct {
	// Name is the skill's stable identifier (kebab-case).
	Name string `yaml:"name"`

	// Path is the directory inside the hub where this skill lives,
	// relative to the hub root. Example: "skills/design-system".
	Path string `yaml:"path"`

	// Default marks skills the wizard pre-selects in Step 2.
	Default bool `yaml:"default,omitempty"`

	// AgentsSupported lists the agent IDs this skill can be installed
	// to ("claude-code", "codex", "copilot", "opencode"). Empty is
	// rejected by Validate.
	AgentsSupported []string `yaml:"agents_supported"`

	// Description is a one-line human-readable summary.
	Description string `yaml:"description,omitempty"`

	// Version is the skill's own version string, semver or
	// year-month. Optional; the hub HEAD commit is authoritative.
	Version string `yaml:"version,omitempty"`

	// MinFDHVersion declares the minimum `fdh` CLI version required.
	// Semver. Skipped by the wizard if the CLI is older.
	MinFDHVersion string `yaml:"min_fdh_version,omitempty"`

	// Tags is a free-form list used for search and filtering.
	Tags []string `yaml:"tags,omitempty"`
}

// LoadOptions controls how Load fetches and caches the hub.
type LoadOptions struct {
	// CacheDir is the directory the hub clone is materialised in.
	// Empty means "use the default per-user cache" (see DefaultCacheDir).
	CacheDir string

	// Branch is the hub branch to track. Empty means "main".
	Branch string

	// SkipFetch disables the lazy `git fetch` after open. Used by
	// tests pointing at a hand-built directory.
	SkipFetch bool

	// Logger receives one-line operational messages. nil discards.
	Logger func(line string)
}
