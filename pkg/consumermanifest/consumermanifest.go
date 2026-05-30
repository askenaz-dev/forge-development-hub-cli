// Package consumermanifest defines the consumer-side
// `.fdh/manifest.yaml` schema and operations:
//
//   - Load: read and decode the file with KnownFields(true).
//   - Validate: enforce schema_version, name, scope, optional profile.
//   - Expand: resolve a manifest against a hub registry into a flat
//     list of ResolvedComponent (profile + extends + explicit
//     entries, deduped by (name, kind)).
//   - GenerateFromLegacy: derive a manifest from on-disk markers
//     when the consumer has no manifest yet.
//
// Boundaries: stdlib + yaml.v3 + pkg/hubregistry + pkg/managed.
// MUST NOT import pkg/adapters or internal/cli.
package consumermanifest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/forge/fdh/pkg/hubregistry"
	"github.com/forge/fdh/pkg/managed"
)

// SupportedSchemaVersion is the only manifest schema this fdh
// release understands. Adding optional fields does NOT bump it.
const SupportedSchemaVersion = 1

// Filename is the manifest's path relative to the consumer repo root.
var Filename = filepath.Join(".fdh", "manifest.yaml")

// Manifest is the consumer's declarative intent.
type Manifest struct {
	SchemaVersion int      `yaml:"schema_version"`
	Profile       string   `yaml:"profile,omitempty"`
	Scope         string   `yaml:"scope,omitempty"`
	Skills        []Entry  `yaml:"skills,omitempty"`
	Rules         []Entry  `yaml:"rules,omitempty"`
	Agents        []Entry  `yaml:"agents,omitempty"`
	Hooks         []Entry  `yaml:"hooks,omitempty"`
	Extends       *Extends `yaml:"extends,omitempty"`
}

// Entry is one component reference in a kind block.
//
// Version is an optional SemVer constraint:
//
//   - "" / "latest" / "*"  → newest published version satisfying any
//     constraint (current behavior — no pinning)
//   - "0.4.0"             → exact match
//   - "^0.4"              → caret: stays within 0.4.x (0.x semantics
//     respected)
//   - "~0.4.1"            → tilde: stays within 0.4.x with floor at
//     0.4.1
type Entry struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version,omitempty"`
}

// Extends supports add/remove modifiers over a base profile.
type Extends struct {
	AddSkills    []Entry `yaml:"add_skills,omitempty"`
	RemoveSkills []Entry `yaml:"remove_skills,omitempty"`
	AddRules     []Entry `yaml:"add_rules,omitempty"`
	RemoveRules  []Entry `yaml:"remove_rules,omitempty"`
	AddAgents    []Entry `yaml:"add_agents,omitempty"`
	RemoveAgents []Entry `yaml:"remove_agents,omitempty"`
	AddHooks     []Entry `yaml:"add_hooks,omitempty"`
	RemoveHooks  []Entry `yaml:"remove_hooks,omitempty"`
}

// ResolvedComponent is one (name, kind) pair the resolver picked,
// enriched with the matching hub catalog entry for materialization.
//
// ConstraintRaw is the raw constraint string from the manifest entry
// (e.g. "^0.4"); empty when the entry had no `version:` field.
// ResolvedVersion is the SemVer the resolver picked from the
// available versions; it MAY differ from HubEntry.Version when the
// catalog publishes multiple versions per component (per the
// wire-protocol multi-version path).
type ResolvedComponent struct {
	Name            string
	Kind            string
	HubEntry        *hubregistry.ComponentEntry
	FromProfile     bool
	ConstraintRaw   string
	ResolvedVersion string
}

var kebabRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Load reads and decodes <rootDir>/.fdh/manifest.yaml. Returns
// os.ErrNotExist when the manifest doesn't exist; the caller is
// expected to attempt legacy auto-generation.
func Load(rootDir string) (*Manifest, error) {
	path := filepath.Join(rootDir, Filename)
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("consumermanifest: parse %s: %w", path, err)
	}
	return &m, nil
}

// Validate enforces the schema rules.
func Validate(m *Manifest) error {
	if m == nil {
		return errors.New("consumermanifest.Validate: nil manifest")
	}
	if m.SchemaVersion != SupportedSchemaVersion {
		return fmt.Errorf("consumermanifest: schema_version %d not supported (this fdh supports %d)",
			m.SchemaVersion, SupportedSchemaVersion)
	}
	if m.Scope != "" && m.Scope != "project" && m.Scope != "user" {
		return fmt.Errorf("consumermanifest: scope %q invalid (want \"project\" or \"user\")", m.Scope)
	}
	for _, ent := range m.Skills {
		if err := validateEntry("skills", ent); err != nil {
			return err
		}
	}
	for _, ent := range m.Rules {
		if err := validateEntry("rules", ent); err != nil {
			return err
		}
	}
	for _, ent := range m.Agents {
		if err := validateEntry("agents", ent); err != nil {
			return err
		}
	}
	for _, ent := range m.Hooks {
		if err := validateEntry("hooks", ent); err != nil {
			return err
		}
	}
	if m.Extends != nil {
		all := [][]Entry{
			m.Extends.AddSkills, m.Extends.RemoveSkills,
			m.Extends.AddRules, m.Extends.RemoveRules,
			m.Extends.AddAgents, m.Extends.RemoveAgents,
			m.Extends.AddHooks, m.Extends.RemoveHooks,
		}
		for _, set := range all {
			for _, ent := range set {
				if err := validateEntry("extends", ent); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateEntry(block string, e Entry) error {
	if e.Name == "" {
		return fmt.Errorf("consumermanifest: %s entry missing 'name'", block)
	}
	if !kebabRE.MatchString(e.Name) {
		return fmt.Errorf("consumermanifest: %s entry %q must be kebab-case", block, e.Name)
	}
	if _, err := ParseConstraint(e.Version); err != nil {
		return fmt.Errorf("consumermanifest: %s entry %q has invalid version constraint %q: %w", block, e.Name, e.Version, err)
	}
	return nil
}

// ProfileLookup is a function the caller provides to resolve a
// profile name to its list of (name, kind) members. Profiles are
// declared by the hub in `hub/profiles.yaml`; resolving them is the
// caller's job because this package does not know how the hub stores
// profiles. Returns os.ErrNotExist (or a typed error) if the profile
// name is unknown.
type ProfileLookup func(profile string) ([]ProfileMember, error)

// ProfileMember is one member of a hub profile.
type ProfileMember struct {
	Name string
	Kind string
}

// Expand resolves the manifest against a hub catalog and an optional
// profile resolver. Result is sorted by (kind, name).
//
// Resolution algorithm:
//  1. Start with the profile members (if any), each marked
//     FromProfile=true.
//  2. Apply Extends.Remove_* (remove from the set).
//  3. Apply Extends.Add_* (add to the set; preserve membership; not
//     marked FromProfile).
//  4. Add explicit manifest entries (Skills/Rules/Agents/Hooks).
//  5. Dedup by (name, kind); explicit/add entries override the
//     profile-derived membership flag only by preserving the most
//     authoritative source (explicit > add > profile).
//  6. For each surviving (name, kind), look up the hub entry. If
//     missing, return error naming the unresolved tuple.
//
// profileLookup may be nil if the manifest declares no profile.
func Expand(m *Manifest, reg *hubregistry.Registry, profileLookup ProfileLookup) ([]ResolvedComponent, error) {
	if m == nil {
		return nil, errors.New("consumermanifest.Expand: nil manifest")
	}
	if reg == nil {
		return nil, errors.New("consumermanifest.Expand: nil registry")
	}

	type key struct{ name, kind string }
	set := map[key]ResolvedComponent{}

	if m.Profile != "" {
		if profileLookup == nil {
			return nil, fmt.Errorf("consumermanifest.Expand: manifest declares profile %q but no profile lookup was provided", m.Profile)
		}
		members, err := profileLookup(m.Profile)
		if err != nil {
			return nil, fmt.Errorf("consumermanifest.Expand: profile %q: %w", m.Profile, err)
		}
		for _, mem := range members {
			set[key{mem.Name, mem.Kind}] = ResolvedComponent{
				Name: mem.Name, Kind: mem.Kind, FromProfile: true,
			}
		}
	}

	if m.Extends != nil {
		// Remove first so an add can re-introduce a removed item.
		for _, e := range m.Extends.RemoveSkills {
			delete(set, key{e.Name, managed.KindSkill})
		}
		for _, e := range m.Extends.RemoveRules {
			delete(set, key{e.Name, managed.KindRule})
		}
		for _, e := range m.Extends.RemoveAgents {
			delete(set, key{e.Name, managed.KindAgent})
		}
		for _, e := range m.Extends.RemoveHooks {
			delete(set, key{e.Name, managed.KindHook})
		}
		// Adds.
		for _, e := range m.Extends.AddSkills {
			set[key{e.Name, managed.KindSkill}] = ResolvedComponent{Name: e.Name, Kind: managed.KindSkill}
		}
		for _, e := range m.Extends.AddRules {
			set[key{e.Name, managed.KindRule}] = ResolvedComponent{Name: e.Name, Kind: managed.KindRule}
		}
		for _, e := range m.Extends.AddAgents {
			set[key{e.Name, managed.KindAgent}] = ResolvedComponent{Name: e.Name, Kind: managed.KindAgent}
		}
		for _, e := range m.Extends.AddHooks {
			set[key{e.Name, managed.KindHook}] = ResolvedComponent{Name: e.Name, Kind: managed.KindHook}
		}
	}

	// Explicit entries (highest priority — overrides profile mark).
	for _, e := range m.Skills {
		set[key{e.Name, managed.KindSkill}] = ResolvedComponent{Name: e.Name, Kind: managed.KindSkill, ConstraintRaw: e.Version}
	}
	for _, e := range m.Rules {
		set[key{e.Name, managed.KindRule}] = ResolvedComponent{Name: e.Name, Kind: managed.KindRule, ConstraintRaw: e.Version}
	}
	for _, e := range m.Agents {
		set[key{e.Name, managed.KindAgent}] = ResolvedComponent{Name: e.Name, Kind: managed.KindAgent, ConstraintRaw: e.Version}
	}
	for _, e := range m.Hooks {
		set[key{e.Name, managed.KindHook}] = ResolvedComponent{Name: e.Name, Kind: managed.KindHook, ConstraintRaw: e.Version}
	}

	// Resolve against the hub catalog (single-version catalog view).
	out := make([]ResolvedComponent, 0, len(set))
	for _, rc := range set {
		entry := reg.ComponentByName(rc.Name, rc.Kind)
		if entry == nil {
			return nil, fmt.Errorf("consumermanifest.Expand: component %q (kind=%s) not found in hub catalog", rc.Name, rc.Kind)
		}
		rc.HubEntry = entry
		// Apply the constraint against the catalog entry's published
		// version. The hubregistry view only exposes one version per
		// component; multi-version resolution requires the wire-
		// protocol registry (HTTPRegistry.Manifest) and is handled by
		// the caller when the manifest references a constraint that
		// the catalog's single version cannot satisfy.
		constraint, err := ParseConstraint(rc.ConstraintRaw)
		if err != nil {
			return nil, fmt.Errorf("consumermanifest.Expand: component %q (kind=%s) has invalid version constraint %q: %w",
				rc.Name, rc.Kind, rc.ConstraintRaw, err)
		}
		if entry.Version != "" {
			if !constraint.Matches(entry.Version) {
				return nil, fmt.Errorf("consumermanifest.Expand: component %q (kind=%s) constraint %q unsatisfiable against catalog version %q (multi-version resolution requires the wire-protocol path; the hubregistry view exposes only one version)",
					rc.Name, rc.Kind, rc.ConstraintRaw, entry.Version)
			}
			rc.ResolvedVersion = entry.Version
		}
		out = append(out, rc)
	}
	// Stable order: kind then name.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// GenerateFromLegacy walks rootDir looking for legacy markers
// (`.skill-version`, `.skill-version-<name>`) or the new
// `.fdh-managed.yaml` markers and derives a v1 manifest from them.
// The returned manifest's entries are sorted alphabetically per kind
// for determinism. Returns an empty manifest (`SchemaVersion: 1`,
// no entries) when nothing is found — the caller decides whether
// that's an error.
func GenerateFromLegacy(rootDir string) (*Manifest, error) {
	m := &Manifest{SchemaVersion: SupportedSchemaVersion}
	type key struct{ name, kind string }
	seen := map[key]struct{}{}

	walkDirs := []string{
		filepath.Join(rootDir, ".claude"),
		filepath.Join(rootDir, ".codex"),
		filepath.Join(rootDir, ".github"),
		filepath.Join(rootDir, ".opencode"),
	}
	for _, dir := range walkDirs {
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil //nolint:nilerr // tolerate missing or read-perm errors
			}
			if d.IsDir() {
				return nil
			}
			if !managed.IsAnyMarkerFilename(d.Name()) {
				return nil
			}
			mm, err := managed.Read(p)
			if err != nil {
				return nil //nolint:nilerr
			}
			if mm.Name == "" {
				return nil
			}
			kind := mm.Kind
			if kind == "" {
				kind = managed.KindSkill
			}
			if _, dup := seen[key{mm.Name, kind}]; dup {
				return nil
			}
			seen[key{mm.Name, kind}] = struct{}{}
			ent := Entry{Name: mm.Name}
			switch kind {
			case managed.KindSkill:
				m.Skills = append(m.Skills, ent)
			case managed.KindRule:
				m.Rules = append(m.Rules, ent)
			case managed.KindAgent:
				m.Agents = append(m.Agents, ent)
			case managed.KindHook:
				m.Hooks = append(m.Hooks, ent)
			}
			return nil
		})
	}
	sort.Slice(m.Skills, func(i, j int) bool { return m.Skills[i].Name < m.Skills[j].Name })
	sort.Slice(m.Rules, func(i, j int) bool { return m.Rules[i].Name < m.Rules[j].Name })
	sort.Slice(m.Agents, func(i, j int) bool { return m.Agents[i].Name < m.Agents[j].Name })
	sort.Slice(m.Hooks, func(i, j int) bool { return m.Hooks[i].Name < m.Hooks[j].Name })
	return m, nil
}

// Write serializes m to <rootDir>/.fdh/manifest.yaml. Creates the
// `.fdh/` directory if needed. Output is LF-only and stable.
func Write(rootDir string, m *Manifest) error {
	if m == nil {
		return errors.New("consumermanifest.Write: nil manifest")
	}
	body, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("consumermanifest.Write: marshal: %w", err)
	}
	// Normalize CRLF→LF defensively (yaml.v3 already emits LF).
	body = []byte(strings.ReplaceAll(string(body), "\r\n", "\n"))
	dir := filepath.Join(rootDir, ".fdh")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("consumermanifest.Write: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(rootDir, Filename)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("consumermanifest.Write: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("consumermanifest.Write: rename %s: %w", path, err)
	}
	return nil
}

// HasAnyEntries reports whether the manifest declares at least one
// component (in any kind block, profile, or extends-add).
func HasAnyEntries(m *Manifest) bool {
	if m == nil {
		return false
	}
	if len(m.Skills)+len(m.Rules)+len(m.Agents)+len(m.Hooks) > 0 {
		return true
	}
	if m.Profile != "" {
		return true
	}
	if m.Extends != nil {
		if len(m.Extends.AddSkills)+len(m.Extends.AddRules)+
			len(m.Extends.AddAgents)+len(m.Extends.AddHooks) > 0 {
			return true
		}
	}
	return false
}
