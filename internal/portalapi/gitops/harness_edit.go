package gitops

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// HarnessEdit is the set of mutations applied to one harness in
// hub/harnesses.yaml by a harness-edit web action. Every field is optional; a
// nil slice/pointer means "leave unchanged". Add/Remove are applied per kind.
type HarnessEdit struct {
	// Description / OwnerTeam, when non-nil, replace the harness's scalars.
	Description *string
	OwnerTeam   *string

	// AddSkills / RemoveSkills (and the rule/agent/hook analogs) add or remove
	// component names under the harness's per-kind sequence.
	AddSkills, RemoveSkills []string
	AddRules, RemoveRules   []string
	AddAgents, RemoveAgents []string
	AddHooks, RemoveHooks   []string
}

// isEmpty reports whether the edit would change nothing.
func (e HarnessEdit) isEmpty() bool {
	return e.Description == nil && e.OwnerTeam == nil &&
		len(e.AddSkills) == 0 && len(e.RemoveSkills) == 0 &&
		len(e.AddRules) == 0 && len(e.RemoveRules) == 0 &&
		len(e.AddAgents) == 0 && len(e.RemoveAgents) == 0 &&
		len(e.AddHooks) == 0 && len(e.RemoveHooks) == 0
}

// applyHarnessEdit edits hub/harnesses.yaml in the node tree: it locates the
// named harness mapping under `harnesses:` and applies the edit's scalar
// replacements and per-kind add/remove sets, then re-serializes. An absent
// harness is an error (the editor only edits existing harnesses; harness
// creation is out of scope for this composer).
func applyHarnessEdit(harnessData []byte, harnessName string, edit HarnessEdit) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(harnessData, &root); err != nil {
		return nil, fmt.Errorf("parse harnesses.yaml: %w", err)
	}
	h := findHarnessNode(&root, harnessName)
	if h == nil {
		return nil, fmt.Errorf("harnesses.yaml has no harness named %q", harnessName)
	}

	if edit.Description != nil {
		setOrAddMappingString(h, "description", *edit.Description)
	}
	if edit.OwnerTeam != nil {
		setOrAddMappingString(h, "owner_team", *edit.OwnerTeam)
	}

	editKind(h, "skills", edit.AddSkills, edit.RemoveSkills)
	editKind(h, "rules", edit.AddRules, edit.RemoveRules)
	editKind(h, "agents", edit.AddAgents, edit.RemoveAgents)
	editKind(h, "hooks", edit.AddHooks, edit.RemoveHooks)

	return marshalNode(&root)
}

// syncDefaultHarness adds or removes `name` from the `default` harness's
// per-kind sequence (skills/rules/agents/hooks), used by curate to keep the
// `default` harness mirroring the registry's default:true set atomically (D6).
// add=true adds; add=false removes. Returns the edited bytes. An absent
// `default` harness is an error.
func syncDefaultHarness(harnessData []byte, kind, name string, add bool) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(harnessData, &root); err != nil {
		return nil, fmt.Errorf("parse harnesses.yaml: %w", err)
	}
	h := findHarnessNode(&root, "default")
	if h == nil {
		return nil, fmt.Errorf("harnesses.yaml has no `default` harness to sync")
	}
	seqKey := kindPlural(kind)
	if add {
		editKind(h, seqKey, []string{name}, nil)
	} else {
		editKind(h, seqKey, nil, []string{name})
	}
	return marshalNode(&root)
}

// harnessHasComponent reports whether the `default` harness already lists name
// under the kind's sequence — used to make the default-sync idempotent in the
// composer (avoid adding a duplicate / removing an absent name).
func harnessHasComponent(harnessData []byte, harness, kind, name string) (bool, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(harnessData, &root); err != nil {
		return false, fmt.Errorf("parse harnesses.yaml: %w", err)
	}
	h := findHarnessNode(&root, harness)
	if h == nil {
		return false, nil
	}
	seq := mappingValueNode(h, kindPlural(kind))
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return false, nil
	}
	for _, item := range seq.Content {
		if item.Value == name {
			return true, nil
		}
	}
	return false, nil
}

// findHarnessNode locates the mapping node for a harness under `harnesses:`.
func findHarnessNode(root *yaml.Node, name string) *yaml.Node {
	doc := documentRoot(root)
	harnesses := mappingValueNode(doc, "harnesses")
	if harnesses == nil || harnesses.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(harnesses.Content); i += 2 {
		if harnesses.Content[i].Value == name {
			return harnesses.Content[i+1]
		}
	}
	return nil
}

// editKind applies add/remove to a harness's per-kind sequence (e.g. "skills").
// Adds that already exist are skipped (idempotent); removes of absent names are
// no-ops. The sequence is created if absent and a non-empty add is requested.
// The resulting list is kept stable (insertion order preserved; adds appended).
func editKind(h *yaml.Node, seqKey string, add, remove []string) {
	if len(add) == 0 && len(remove) == 0 {
		return
	}
	seq := mappingValueNode(h, seqKey)
	if seq == nil {
		if len(add) == 0 {
			return // nothing to remove from a non-existent sequence
		}
		seq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		h.Content = append(h.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: seqKey},
			seq,
		)
	}

	removeSet := map[string]struct{}{}
	for _, r := range remove {
		removeSet[r] = struct{}{}
	}
	present := map[string]struct{}{}
	kept := seq.Content[:0]
	for _, item := range seq.Content {
		if _, drop := removeSet[item.Value]; drop {
			continue
		}
		present[item.Value] = struct{}{}
		kept = append(kept, item)
	}
	seq.Content = kept

	// Append adds that are not already present, in a deterministic order.
	toAdd := make([]string, 0, len(add))
	for _, a := range add {
		if _, exists := present[a]; exists {
			continue
		}
		present[a] = struct{}{}
		toAdd = append(toAdd, a)
	}
	sort.Strings(toAdd)
	for _, a := range toAdd {
		seq.Content = append(seq.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: a})
	}
}
