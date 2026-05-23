// Package adapters loads the manifest-driven agent path map.
//
// Each supported agent (Claude Code, Copilot, Codex, OpenCode) is described
// by a YAML entry declaring detection probes and the user-scope/project-scope
// directories the agent reads. The embedded default manifest is shipped with
// the binary via go:embed; a per-user override file may replace individual
// agent entries.
//
// The package is the single source of truth for "where does the installer
// write a skill so that <agent> sees it?". Adding a new agent is a YAML edit
// (in builtin.yaml or a user override), not a Go code change.
package adapters

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed builtin.yaml
var builtinYAML []byte

// Scope identifies which scope a path or install targets.
type Scope string

const (
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
)

// AgentEntry describes one agent. Marshalled from / to YAML directly.
type AgentEntry struct {
	ID           string        `yaml:"id"`
	DisplayName  string        `yaml:"display_name"`
	SourceDocURL string        `yaml:"source_doc_url"`
	VerifiedOn   string        `yaml:"verified_on,omitempty"`
	Detect       []Probe       `yaml:"detect"`
	Paths        ScopedPaths   `yaml:"paths"`
}

// ScopedPaths holds the user-scope and project-scope read paths the agent
// looks at. Each entry uses "<name>" as a placeholder substituted with the
// skill's directory name at install time. Paths beginning with "~" expand
// to the user's home.
type ScopedPaths struct {
	User    []string `yaml:"user"`
	Project []string `yaml:"project"`
}

// Probe describes one detection check. The "type" field switches behaviour;
// see probe_types.go for the type registry.
type Probe struct {
	Type string `yaml:"type"`
	Path string `yaml:"path,omitempty"`
	Name string `yaml:"name,omitempty"`
	Cmd  string `yaml:"cmd,omitempty"`
}

// Manifest is the deserialized YAML document.
type Manifest struct {
	Agents []AgentEntry `yaml:"agents"`
}

// LoadDefault returns the manifest derived from the embedded builtin.yaml.
// It does NOT read any override file.
func LoadDefault() (*Manifest, error) {
	return parse(builtinYAML)
}

// LoadWithOverride returns the manifest produced by merging the embedded
// default with the user override file at overridePath (if it exists).
//
// Merge rule (per the agent-adapter-map spec):
//   - For each agent in the override, the override entry FULLY REPLACES
//     any entry in the embedded default with the same ID.
//   - Agents in the embedded default not mentioned by the override are
//     preserved verbatim.
//
// A missing override file is not an error.
// An override file that fails to parse IS an error and stops the load.
func LoadWithOverride(overridePath string) (*Manifest, error) {
	def, err := LoadDefault()
	if err != nil {
		return nil, fmt.Errorf("load embedded default: %w", err)
	}

	raw, err := os.ReadFile(overridePath)
	if err != nil {
		if os.IsNotExist(err) {
			return def, nil
		}
		return nil, fmt.Errorf("read override %s: %w", overridePath, err)
	}

	override, err := parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse override %s: %w", overridePath, err)
	}

	return mergeManifests(def, override), nil
}

func parse(raw []byte) (*Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("decode YAML: %w", err)
	}
	if err := m.validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) validate() error {
	if len(m.Agents) == 0 {
		return fmt.Errorf("manifest has no agents")
	}
	seen := map[string]bool{}
	for i, a := range m.Agents {
		if a.ID == "" {
			return fmt.Errorf("agents[%d]: id is required", i)
		}
		if !isKebabCase(a.ID) {
			return fmt.Errorf("agents[%d]: id %q must be kebab-case", i, a.ID)
		}
		if seen[a.ID] {
			return fmt.Errorf("duplicate agent id %q", a.ID)
		}
		seen[a.ID] = true

		if a.DisplayName == "" {
			return fmt.Errorf("agents[%s]: display_name is required", a.ID)
		}
		if len(a.Detect) == 0 {
			return fmt.Errorf("agents[%s]: at least one detect probe is required", a.ID)
		}
		for j, p := range a.Detect {
			if err := validateProbe(p); err != nil {
				return fmt.Errorf("agents[%s].detect[%d]: %w", a.ID, j, err)
			}
		}
		if len(a.Paths.User) == 0 && len(a.Paths.Project) == 0 {
			return fmt.Errorf("agents[%s]: must declare at least one path", a.ID)
		}
	}
	return nil
}

func mergeManifests(base, override *Manifest) *Manifest {
	out := &Manifest{}
	overrideByID := map[string]AgentEntry{}
	for _, a := range override.Agents {
		overrideByID[a.ID] = a
	}

	used := map[string]bool{}
	for _, a := range base.Agents {
		if rep, ok := overrideByID[a.ID]; ok {
			out.Agents = append(out.Agents, rep)
			used[a.ID] = true
		} else {
			out.Agents = append(out.Agents, a)
		}
	}
	// Append any override agents that weren't in the base.
	for _, a := range override.Agents {
		if !used[a.ID] {
			out.Agents = append(out.Agents, a)
		}
	}
	return out
}

// AgentIDs returns the agent IDs in the order they appear in the manifest.
func (m *Manifest) AgentIDs() []string {
	out := make([]string, 0, len(m.Agents))
	for _, a := range m.Agents {
		out = append(out, a.ID)
	}
	return out
}

// AgentByID returns the agent entry with the given ID, or nil.
func (m *Manifest) AgentByID(id string) *AgentEntry {
	for i := range m.Agents {
		if m.Agents[i].ID == id {
			return &m.Agents[i]
		}
	}
	return nil
}

// ResolvedPath represents a destination path with the agents it serves.
type ResolvedPath struct {
	// Path is the absolute filesystem path of the directory the bundle
	// should be written to (the bundle's directory, NOT the parent
	// skills/ directory). For project scope this is project-root-relative
	// before expansion and absolute after.
	Path string

	// Agents lists the agent IDs that read from this path. A four-agent
	// install at project scope produces three ResolvedPath entries; each
	// of them is satisfied by a subset of the requested agents.
	Agents []string
}

// PathSetOptions configures how PathSet computes the union.
type PathSetOptions struct {
	// SkillName is substituted for "<name>" in path templates.
	SkillName string

	// ProjectRoot is the absolute path used to anchor project-scope paths.
	// Required when Scope == ScopeProject.
	ProjectRoot string

	// HomeDir is the absolute path used to expand "~" prefixes. Required
	// when any user-scope path is requested.
	HomeDir string

	// Scope picks user-scope or project-scope read paths.
	Scope Scope

	// AgentIDs is the set of agents the install is targeting. The function
	// returns the union of paths these agents read, deduplicated.
	AgentIDs []string
}

// PathSet returns the deduplicated union of destination paths for the
// requested agents at the chosen scope. Each ResolvedPath is annotated with
// the agent IDs it satisfies, so the install pipeline can write a single
// .skill-meta.yaml sidecar per path that lists every agent the path covers.
func (m *Manifest) PathSet(opts PathSetOptions) ([]ResolvedPath, error) {
	if opts.SkillName == "" {
		return nil, fmt.Errorf("PathSet: SkillName is required")
	}
	if opts.Scope == ScopeProject && opts.ProjectRoot == "" {
		return nil, fmt.Errorf("PathSet: ProjectRoot is required for project scope")
	}

	// Collect unique paths in agent-iteration order.
	type acc struct {
		index  int
		agents []string
	}
	bucket := map[string]*acc{}
	order := []string{}

	for _, agentID := range opts.AgentIDs {
		agent := m.AgentByID(agentID)
		if agent == nil {
			return nil, fmt.Errorf("unknown agent id: %q", agentID)
		}
		var rawPaths []string
		switch opts.Scope {
		case ScopeUser:
			rawPaths = agent.Paths.User
		case ScopeProject:
			rawPaths = agent.Paths.Project
		default:
			return nil, fmt.Errorf("unknown scope: %q", opts.Scope)
		}
		for _, raw := range rawPaths {
			expanded, err := ExpandPath(raw, opts.HomeDir, opts.ProjectRoot, opts.SkillName)
			if err != nil {
				return nil, err
			}
			if a, ok := bucket[expanded]; ok {
				if !containsString(a.agents, agentID) {
					a.agents = append(a.agents, agentID)
				}
			} else {
				order = append(order, expanded)
				bucket[expanded] = &acc{index: len(order) - 1, agents: []string{agentID}}
			}
		}
	}

	out := make([]ResolvedPath, len(order))
	for path, a := range bucket {
		sort.Strings(a.agents) // stable agent listing per path
		out[a.index] = ResolvedPath{Path: path, Agents: a.agents}
	}
	return out, nil
}

// ExpandPath substitutes "<name>" with skillName, expands a leading "~/" to
// homeDir, and anchors non-absolute paths to projectRoot when projectRoot is
// non-empty. Returns an absolute, cleaned path.
func ExpandPath(p, homeDir, projectRoot, skillName string) (string, error) {
	expanded := strings.ReplaceAll(p, "<name>", skillName)

	switch {
	case strings.HasPrefix(expanded, "~/") || expanded == "~":
		if homeDir == "" {
			return "", fmt.Errorf("expand %q: home directory not provided", p)
		}
		rest := strings.TrimPrefix(expanded, "~")
		expanded = filepath.Join(homeDir, rest)
	case filepath.IsAbs(expanded):
		// already absolute
	default:
		// Relative path -> anchor at project root if available.
		if projectRoot == "" {
			return "", fmt.Errorf("expand %q: relative path but no project root", p)
		}
		expanded = filepath.Join(projectRoot, expanded)
	}
	// Drop trailing slashes and clean.
	expanded = filepath.Clean(expanded)
	return expanded, nil
}

func containsString(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func isKebabCase(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			// ok
		case r == '-':
			if i == 0 || i == len(s)-1 {
				return false
			}
		default:
			return false
		}
	}
	// reject consecutive hyphens
	return !strings.Contains(s, "--")
}
