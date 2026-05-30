// Package hubregistry parses and serves the catalog at
// `hub/registry.yaml` (schema v2) inside a forge Development Hub clone.
// During the transition window (~2026-07-22) it also accepts the
// `schema_version: 1` mirror at `skills/registry.yaml`.
//
// The hub is a Git repository. This package clones (or pulls) it into a
// per-user cache, decodes the YAML, validates it, and exposes the
// entries the rest of the CLI consumes (`fdh init` wizard, `fdh
// update`, `fdh validate-registry`, the future manifest/lock resolver).
//
// Boundaries: stdlib + go-git + yaml.v3 only. NO imports from
// `internal/cli` or `pkg/adapters`.
package hubregistry

import "time"

// SchemaVersion is the catalog schema version this package targets.
// The hub spec `hub-registry-v2` declared v2 as the only writable
// version; v1 is read-only via the legacy mirror normalizer.
const SchemaVersion = 2

// Registry is the in-memory shape of the catalog plus runtime
// metadata.
type Registry struct {
	// SchemaVersion is the value declared at the top of the catalog
	// file. 2 = native v2 (`components:`); 1 = legacy mirror that was
	// normalized to v2 in memory.
	SchemaVersion int `yaml:"schema_version"`

	// HubVersion is the free-form release marker declared by the hub.
	// Empty when loaded from a v1 mirror.
	HubVersion string `yaml:"hub_version,omitempty"`

	// GeneratedAt is the ISO-8601 timestamp the hub recorded when it
	// regenerated the catalog. Optional.
	GeneratedAt time.Time `yaml:"generated_at,omitempty"`

	// Components is the unified catalog (skills, rules, agents, hooks).
	// In v2 this is decoded directly; in v1 it is normalized from
	// `Skills` with Kind="skill".
	Components []ComponentEntry `yaml:"components,omitempty"`

	// Skills is a derived view of Components filtered by Kind=="skill".
	// Populated by Load post-decode; not serialized.
	//
	// Deprecated: use Components or ComponentsByKind("skill") instead.
	// Kept during the v1→v2 transition window; removed in a follow-up
	// once external call sites have migrated.
	Skills []SkillEntry `yaml:"-"`

	// LocalPath is the absolute on-disk path of the hub clone.
	// Populated by Load. Not serialized.
	LocalPath string `yaml:"-"`

	// HubCommit is the SHA of HEAD at load time. Populated by Load.
	HubCommit string `yaml:"-"`
}

// ComponentEntry is one entry in the v2 catalog.
type ComponentEntry struct {
	// Name is the component's stable identifier (kebab-case). Unique
	// within the same Kind.
	Name string `yaml:"name"`

	// Kind is one of: skill, rule, agent, hook. Required (no default).
	Kind string `yaml:"kind"`

	// Path is the directory inside the hub where this component lives,
	// relative to the hub root. MUST be coherent with Kind
	// (skill→skills/<name>, rule→rules/<name>, etc.).
	Path string `yaml:"path"`

	// OwnerTeam is the team responsible for the component.
	OwnerTeam string `yaml:"owner_team,omitempty"`

	// Description is a one-line human-readable summary.
	Description string `yaml:"description,omitempty"`

	// Default marks components the wizard pre-selects in the
	// components step.
	Default bool `yaml:"default,omitempty"`

	// MinFDHVersion declares the minimum `fdh` CLI version required.
	// Semver. Skipped by the wizard if the CLI is older.
	MinFDHVersion string `yaml:"min_fdh_version,omitempty"`

	// AgentsSupported lists the agent IDs this component can be
	// installed to ("claude-code", "codex", "copilot", "opencode").
	// Non-empty; rejected by Validate otherwise.
	AgentsSupported []string `yaml:"agents_supported"`

	// Version is the component's own SemVer version. Optional in the
	// catalog (frontmatter is authoritative per hub-registry-v2);
	// echoed here when present.
	Version string `yaml:"version,omitempty"`

	// Tags is a free-form list used for search and filtering.
	Tags []string `yaml:"tags,omitempty"`
}

// SkillEntry is one entry in the legacy v1 catalog shape.
//
// Deprecated: kept as a derived view during the v1→v2 transition
// window. New code should use ComponentEntry (filter by
// Kind="skill" where needed).
type SkillEntry struct {
	Name            string   `yaml:"name"`
	Path            string   `yaml:"path"`
	Default         bool     `yaml:"default,omitempty"`
	AgentsSupported []string `yaml:"agents_supported"`
	Description     string   `yaml:"description,omitempty"`
	Version         string   `yaml:"version,omitempty"`
	MinFDHVersion   string   `yaml:"min_fdh_version,omitempty"`
	Tags            []string `yaml:"tags,omitempty"`
}

// LoadOptions controls how Load fetches and caches the hub.
type LoadOptions struct {
	// CacheDir is the directory the hub clone is materialized in.
	// Empty means "use the default per-user cache" (see DefaultCacheDir).
	CacheDir string

	// Branch is the hub branch to track. Empty means "main".
	Branch string

	// SkipFetch disables the lazy `git fetch` after open. Used by
	// tests pointing at a hand-built directory.
	SkipFetch bool

	// Logger receives one-line operational messages. nil discards.
	// The v1→v2 normalizer emits its deprecation warning here.
	Logger func(line string)
}

// Kind constants.
const (
	KindSkill = "skill"
	KindRule  = "rule"
	KindAgent = "agent"
	KindHook  = "hook"
)

// AllKinds is the canonical, ordered list.
var AllKinds = []string{KindSkill, KindRule, KindAgent, KindHook}

// kindDir maps a kind to its top-level directory under the hub.
var kindDir = map[string]string{
	KindSkill: "skills",
	KindRule:  "rules",
	KindAgent: "agents",
	KindHook:  "hooks",
}

// kindOK reports whether s is a known kind.
func kindOK(s string) bool {
	_, ok := kindDir[s]
	return ok
}

// KindDir returns the top-level hub directory plural for a kind
// ("skills", "rules", "agents", "hooks"). Empty for unknown kinds.
func KindDir(kind string) string { return kindDir[kind] }
