package gitops

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file holds the registry.yaml / harnesses.yaml edit helpers used by the
// composers. Two strategies are used deliberately:
//
//   - IMPORT appends a new component entry as TEXT at EOF, byte-for-byte
//     mirroring the CLI's registryEntryYAML (internal/cli/authoring.go) so the
//     web import and `fdh share` produce identical registry deltas, and the
//     file's leading comments are preserved.
//   - CURATE / HARNESS edits parse the YAML node tree, mutate the targeted
//     scalar(s)/sequence(s) in place, and re-serialize. Editing the node tree
//     (not a typed round-trip) keeps the change minimal and YAML-valid; the
//     top-of-file comments survive because yaml.Node carries them.

// registryEntryYAML renders a v2 component entry for hub/registry.yaml, byte-
// identical to internal/cli/authoring.go's registryEntryYAML so the web import
// and the CLI `share` emit the same text. Always default:false — a contribution
// is never auto-adopted.
func registryEntryYAML(kind, name, desc, ownerTeam string, agents []string) string {
	if ownerTeam == "" {
		ownerTeam = "unassigned"
	}
	if len(agents) == 0 {
		if kind == "skill" {
			agents = []string{"claude-code", "codex", "copilot", "opencode"}
		} else {
			agents = []string{"claude-code"}
		}
	}
	descEsc := strings.ReplaceAll(desc, `"`, `'`)
	var b strings.Builder
	fmt.Fprintf(&b, "\n  - name: %s\n", name)
	fmt.Fprintf(&b, "    kind: %s\n", kind)
	fmt.Fprintf(&b, "    description: \"%s\"\n", descEsc)
	fmt.Fprintf(&b, "    owner_team: %s\n", ownerTeam)
	fmt.Fprintf(&b, "    tags: []\n")
	fmt.Fprintf(&b, "    default: false\n")
	fmt.Fprintf(&b, "    min_fdh_version: \"0.4.0\"\n")
	fmt.Fprintf(&b, "    agents_supported: [%s]\n", strings.Join(agents, ", "))
	fmt.Fprintf(&b, "    path: %s/%s\n", kindPlural(kind), name)
	return b.String()
}

// appendRegistryEntry returns the registry bytes with a new entry appended at
// EOF, mirroring the CLI's os.WriteFile(append(regData, entry)) in runKindShare.
func appendRegistryEntry(regData []byte, kind, name, desc, ownerTeam string, agents []string) []byte {
	entry := registryEntryYAML(kind, name, desc, ownerTeam, agents)
	return append(append([]byte(nil), regData...), []byte(entry)...)
}

// kindPlural maps a singular kind to its directory/plural form. Mirrors the CLI.
func kindPlural(kind string) string {
	switch kind {
	case "skill":
		return "skills"
	case "rule":
		return "rules"
	case "agent":
		return "agents"
	case "hook":
		return "hooks"
	}
	return kind + "s"
}

// registryComponentExists reports whether registry.yaml already declares a
// component with the given kind+name (the registry half of the import
// name-collision guard, mirroring the CLI copyTree abort-on-existing-dest).
func registryComponentExists(regData []byte, kind, name string) (bool, error) {
	var cat struct {
		Components []struct {
			Name string `yaml:"name"`
			Kind string `yaml:"kind"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(regData, &cat); err != nil {
		return false, fmt.Errorf("parse registry.yaml: %w", err)
	}
	for _, c := range cat.Components {
		if c.Kind == kind && c.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// setRegistryDefault edits the `default:` flag of the registry entry matching
// kind+name to value, editing the YAML node tree in place. It returns the edited
// bytes and the component's owner_team (needed to mirror the default-harness
// sync). An absent entry is an error.
func setRegistryDefault(regData []byte, kind, name string, value bool) (out []byte, ownerTeam string, err error) {
	var root yaml.Node
	if err := yaml.Unmarshal(regData, &root); err != nil {
		return nil, "", fmt.Errorf("parse registry.yaml: %w", err)
	}
	comp := findComponentNode(&root, kind, name)
	if comp == nil {
		return nil, "", fmt.Errorf("registry has no %s named %q", kind, name)
	}
	ownerTeam = mappingScalar(comp, "owner_team")
	setOrAddMappingBool(comp, "default", value)

	edited, err := marshalNode(&root)
	if err != nil {
		return nil, "", err
	}
	return edited, ownerTeam, nil
}

// versionStatus is the per-version lifecycle state we drive via curate.
const (
	statusActive     = "active"
	statusDeprecated = "deprecated"
	statusYanked     = "yanked"
)

// lifecycleAllowed reports whether moving from `from` to `to` is permitted under
// the forward-only lifecycle active→deprecated→yanked. An empty `from` is
// treated as active (the implicit default for a version without a status).
func lifecycleAllowed(from, to string) bool {
	rank := map[string]int{statusActive: 0, statusDeprecated: 1, statusYanked: 2, "": 0}
	rf, okF := rank[from]
	rt, okT := rank[to]
	if !okF || !okT {
		return false
	}
	// Forward-only: strictly increasing. Re-asserting the same state is a no-op
	// transition we also reject as redundant (callers surface it as such).
	return rt > rf
}

// setVersionStatus edits hub/registry.yaml to set the lifecycle status on a
// component's versions[] entry for the given version. Because the shipped v2
// registry schema does NOT carry an inline versions[] block per component (the
// wire layer derives versions from git tags), lifecycle is recorded on the
// component entry under a `versions:` mapping keyed by version — additive and
// CI-validated. The transition is forward-only; current is the version's
// existing status ("" if none).
//
// It returns the edited bytes and the prior status of that version.
func setVersionStatus(regData []byte, kind, name, version, newStatus string) (out []byte, prior string, err error) {
	var root yaml.Node
	if err := yaml.Unmarshal(regData, &root); err != nil {
		return nil, "", fmt.Errorf("parse registry.yaml: %w", err)
	}
	comp := findComponentNode(&root, kind, name)
	if comp == nil {
		return nil, "", fmt.Errorf("registry has no %s named %q", kind, name)
	}

	versionsNode := mappingValueNode(comp, "versions")
	if versionsNode == nil {
		// Add a `versions:` mapping to the component.
		versionsNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		comp.Content = append(comp.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "versions"},
			versionsNode,
		)
	}

	// Find or create the version key (a mapping {status: <s>}).
	var verEntry *yaml.Node
	for i := 0; i+1 < len(versionsNode.Content); i += 2 {
		if versionsNode.Content[i].Value == version {
			verEntry = versionsNode.Content[i+1]
			break
		}
	}
	if verEntry == nil {
		verEntry = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		versionsNode.Content = append(versionsNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: version},
			verEntry,
		)
	}
	prior = mappingScalar(verEntry, "status")
	setOrAddMappingString(verEntry, "status", newStatus)

	edited, err := marshalNode(&root)
	if err != nil {
		return nil, "", err
	}
	return edited, prior, nil
}

// --- yaml.Node helpers -----------------------------------------------------

// documentRoot unwraps a DocumentNode to its single content node (the mapping).
func documentRoot(root *yaml.Node) *yaml.Node {
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		return root.Content[0]
	}
	return root
}

// findComponentNode locates the mapping node for a component entry matching
// kind+name inside the registry's top-level `components:` sequence.
func findComponentNode(root *yaml.Node, kind, name string) *yaml.Node {
	doc := documentRoot(root)
	comps := mappingValueNode(doc, "components")
	if comps == nil || comps.Kind != yaml.SequenceNode {
		return nil
	}
	for _, item := range comps.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		if mappingScalar(item, "kind") == kind && mappingScalar(item, "name") == name {
			return item
		}
	}
	return nil
}

// mappingValueNode returns the value node for key in a mapping node, or nil.
func mappingValueNode(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mappingScalar returns the scalar string value for key, or "".
func mappingScalar(m *yaml.Node, key string) string {
	v := mappingValueNode(m, key)
	if v == nil || v.Kind != yaml.ScalarNode {
		return ""
	}
	return v.Value
}

func setOrAddMappingBool(m *yaml.Node, key string, value bool) {
	v := mappingValueNode(m, key)
	val := "false"
	if value {
		val = "true"
	}
	if v != nil {
		v.Kind = yaml.ScalarNode
		v.Tag = "!!bool"
		v.Value = val
		v.Style = 0
		return
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: val},
	)
}

func setOrAddMappingString(m *yaml.Node, key, value string) {
	v := mappingValueNode(m, key)
	if v != nil {
		v.Kind = yaml.ScalarNode
		v.Tag = "!!str"
		v.Value = value
		v.Style = 0
		return
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

// marshalNode serializes a node tree with two-space indentation (the repo's
// registry/harness convention).
func marshalNode(root *yaml.Node) ([]byte, error) {
	var sb strings.Builder
	enc := yaml.NewEncoder(&sb)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, fmt.Errorf("encode yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close yaml encoder: %w", err)
	}
	return []byte(sb.String()), nil
}
