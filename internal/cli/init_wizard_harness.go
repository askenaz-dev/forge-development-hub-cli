package cli

// Wizard helpers for the harness-aware, kind-aware Step 0+2 flow.
//
// Lives in a separate file from init_wizard.go to keep the wizard
// orchestration (runInitWizard, prompter interface, huhPrompter)
// readable. Everything here is pure: no I/O outside the registry/
// adapter calls it explicitly accepts.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/forge/fdh/pkg/adapters"
	"github.com/forge/fdh/pkg/consumerlock"
	"github.com/forge/fdh/pkg/consumermanifest"
	"github.com/forge/fdh/pkg/gitignore"
	"github.com/forge/fdh/pkg/hubregistry"
	"github.com/forge/fdh/pkg/managed"
	"github.com/forge/fdh/pkg/state"
)

// harnessChoicesFromDoc converts the parsed harnesses.yaml into the
// wizard's option list. Sorted alphabetically with "default" pinned
// first so it appears at the top of the picker (matching the
// preselect).
func harnessChoicesFromDoc(doc *hubregistry.HarnessesDoc) []harnessChoice {
	if doc == nil {
		return nil
	}
	out := make([]harnessChoice, 0, len(doc.Harnesses))
	for name, h := range doc.Harnesses {
		out = append(out, harnessChoice{
			Name:        name,
			Description: h.Description,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == "default" {
			return true
		}
		if out[j].Name == "default" {
			return false
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// defaultHarnessName returns "default" if that harness exists, else
// the first one alphabetically. Used as the preselect for SelectHarness.
func defaultHarnessName(doc *hubregistry.HarnessesDoc) string {
	if doc == nil || len(doc.Harnesses) == 0 {
		return ""
	}
	if _, ok := doc.Harnesses["default"]; ok {
		return "default"
	}
	names := make([]string, 0, len(doc.Harnesses))
	for n := range doc.Harnesses {
		names = append(names, n)
	}
	sort.Strings(names)
	return names[0]
}

// harnessMemberRefs flattens a harness's per-kind lists into a single
// []componentRef. Empty for an unknown harness name or empty doc.
func harnessMemberRefs(doc *hubregistry.HarnessesDoc, name string) []componentRef {
	if doc == nil || name == "" {
		return nil
	}
	h, ok := doc.Harnesses[name]
	if !ok {
		return nil
	}
	out := make([]componentRef, 0,
		len(h.Skills)+len(h.Rules)+len(h.Agents)+len(h.Hooks))
	for _, n := range h.Skills {
		out = append(out, componentRef{Name: n, Kind: managed.KindSkill})
	}
	for _, n := range h.Rules {
		out = append(out, componentRef{Name: n, Kind: managed.KindRule})
	}
	for _, n := range h.Agents {
		out = append(out, componentRef{Name: n, Kind: managed.KindAgent})
	}
	for _, n := range h.Hooks {
		out = append(out, componentRef{Name: n, Kind: managed.KindHook})
	}
	return out
}

// buildComponentChoices walks the catalog and constructs the wizard's
// Step-2 option list (filtered by agents) plus the pre-selected set
// (harness members + catalog defaults, with deduping). Sorted by
// (kind, name) for stable display.
//
// noDefaults=true drops the catalog-default contribution to the
// preselect; harness members are still pre-checked because they
// represent an explicit pick at Step 0.
func buildComponentChoices(
	reg *hubregistry.Registry,
	agents []string,
	harnessMembers []componentRef,
	noDefaults bool,
) ([]componentChoice, []componentRef) {
	inHarness := map[componentRef]bool{}
	for _, m := range harnessMembers {
		inHarness[m] = true
	}

	choices := []componentChoice{}
	preSet := map[componentRef]bool{}

	for _, c := range reg.Components {
		// Only show components at least one chosen agent supports.
		if !anyShared(c.AgentsSupported, agents) {
			continue
		}
		ref := componentRef{Name: c.Name, Kind: c.Kind}
		choice := componentChoice{
			Name:        c.Name,
			Kind:        c.Kind,
			Description: c.Description,
			Default:     c.Default,
			FromHarness: inHarness[ref],
		}
		choices = append(choices, choice)

		switch {
		case inHarness[ref]:
			preSet[ref] = true
		case c.Default && !noDefaults:
			preSet[ref] = true
		}
	}

	sort.Slice(choices, func(i, j int) bool {
		if choices[i].Kind != choices[j].Kind {
			return choices[i].Kind < choices[j].Kind
		}
		return choices[i].Name < choices[j].Name
	})

	preSelect := make([]componentRef, 0, len(preSet))
	for r := range preSet {
		preSelect = append(preSelect, r)
	}
	sort.Slice(preSelect, func(i, j int) bool {
		if preSelect[i].Kind != preSelect[j].Kind {
			return preSelect[i].Kind < preSelect[j].Kind
		}
		return preSelect[i].Name < preSelect[j].Name
	})
	return choices, preSelect
}

// resolveComponentSelection validates each picked ref against the
// registry. Returns the deduped valid set (preserving picked order)
// and any unknown refs the caller should surface to the user.
//
// `+kind:name` shorthand is normalized (legacy convenience inherited
// from resolveSkillSelection).
func resolveComponentSelection(
	picked []componentRef,
	reg *hubregistry.Registry,
) (valid, unknown []componentRef) {
	seen := map[componentRef]bool{}
	for _, p := range picked {
		p.Name = strings.TrimPrefix(p.Name, "+")
		if seen[p] {
			continue
		}
		seen[p] = true
		if reg.ComponentByName(p.Name, p.Kind) == nil {
			unknown = append(unknown, p)
		} else {
			valid = append(valid, p)
		}
	}
	return
}

// skillNamesFromRefs filters refs down to kind=skill and returns the
// names — used to populate the legacy `selected_skills` JSON field
// in InitResult without breaking the additive-only contract.
func skillNamesFromRefs(refs []componentRef) []string {
	out := []string{}
	for _, r := range refs {
		if r.Kind == managed.KindSkill {
			out = append(out, r.Name)
		}
	}
	return out
}

// summariseComponentSelection renders the Step-3 confirmation body.
// Grouped by kind so the user can scan the bundle at a glance.
func summariseComponentSelection(
	harness string,
	agents []string,
	refs []componentRef,
	dryRun bool,
) string {
	var b strings.Builder
	if dryRun {
		b.WriteString("[dry-run] ")
	}
	if harness != "" {
		fmt.Fprintf(&b, "Harness: %s\n", harness)
	}
	fmt.Fprintf(&b, "Agents:  %s\n", strings.Join(agents, ", "))

	byKind := map[string][]string{}
	for _, r := range refs {
		byKind[r.Kind] = append(byKind[r.Kind], r.Name)
	}
	// Stable order: skill, rule, agent, hook.
	for _, k := range []string{
		managed.KindSkill, managed.KindRule, managed.KindAgent, managed.KindHook,
	} {
		names := byKind[k]
		if len(names) == 0 {
			continue
		}
		sort.Strings(names)
		fmt.Fprintf(&b, "%-7s: %s\n", k+"s", strings.Join(names, ", "))
	}
	return b.String()
}

// installResolvedRefs is the kind-aware install loop the wizard uses
// after Step 3. Mirrors install_manifest.go's installResolvedComponent
// but is hosted here to keep the wizard's InstallOpts/dryRun semantics
// local (the manifest-flow path doesn't expose dry-run today).
//
// Components whose `agents_supported` excludes a chosen agent are
// skipped per-pair without error — that's how `forge-pr-writer` (only
// claude-code) coexists with consumers that selected codex/copilot
// too.
func installResolvedRefs(
	ctx context.Context,
	reg *hubregistry.Registry,
	rc *runContext,
	scope adapters.Scope,
	refs []componentRef,
	agents []string,
	installedByFDH string,
	dryRun bool,
) ([]InstalledSkillResult, error) {
	out := []InstalledSkillResult{}
	for _, r := range refs {
		entry := reg.ComponentByName(r.Name, r.Kind)
		if entry == nil {
			continue // unreachable: resolveComponentSelection filters
		}
		srcDir, err := reg.FetchComponent(ctx, r.Name, r.Kind)
		if err != nil {
			return out, Wrap(ExitRegistryUnreach,
				fmt.Errorf("fetch %s/%s: %w", r.Kind, r.Name, err))
		}
		for _, agentID := range agents {
			if !contains(entry.AgentsSupported, agentID) {
				continue
			}
			res, err := installOneRef(srcDir, r, entry, agentID, rc, scope,
				reg.HubCommit, installedByFDH, dryRun)
			if err != nil {
				return out, Wrap(ExitGenericFailure,
					fmt.Errorf("install %s/%s for %s: %w", r.Kind, r.Name, agentID, err))
			}
			if res == nil {
				continue // kind has no adapter for this agent (e.g. agent kind on copilot)
			}
			out = append(out, InstalledSkillResult{
				Skill:       r.Name,
				Agent:       agentID,
				TargetPath:  res.TargetPath,
				ContentHash: res.ContentHash,
				MarkerPath:  res.MarkerPath,
				Skipped:     res.Skipped,
				Warnings:    res.Warnings,
			})
		}
	}
	return out, nil
}

// installOneRef routes one (component, agent) pair to the right
// adapter family. Returns nil (no error) when the kind has no adapter
// for that agent — caller skips per-pair, doesn't treat as failure.
func installOneRef(
	srcDir string,
	r componentRef,
	entry *hubregistry.ComponentEntry,
	agentID string,
	rc *runContext,
	scope adapters.Scope,
	hubCommit, installedByFDH string,
	dryRun bool,
) (*adapters.InstallResult, error) {
	opts := adapters.InstallOpts{
		SkillName:      r.Name,
		ProjectRoot:    rc.ProjectRoot,
		HomeDir:        rc.HomeDir,
		Scope:          scope,
		HubVersion:     entry.Version,
		HubCommit:      hubCommit,
		InstalledByFDH: installedByFDH,
		DryRun:         dryRun,
	}
	switch r.Kind {
	case managed.KindSkill:
		a := adapters.SkillAdapterByID(agentID)
		if a == nil {
			return nil, nil
		}
		res, err := a.Install(srcDir, opts)
		if err != nil {
			return nil, err
		}
		return &res, nil
	case managed.KindRule:
		a := adapters.RuleAdapterByID(agentID)
		if a == nil {
			return nil, nil
		}
		res, err := a.Install(srcDir, opts)
		if err != nil {
			return nil, err
		}
		return &res, nil
	case managed.KindAgent:
		a := adapters.AgentAdapterByID(agentID)
		if a == nil {
			return nil, nil
		}
		res, err := a.Install(srcDir, opts)
		if err != nil {
			return nil, err
		}
		return &res, nil
	case managed.KindHook:
		a := adapters.HookAdapterByID(agentID)
		if a == nil {
			return nil, nil
		}
		res, err := a.Install(srcDir, opts)
		if err != nil {
			return nil, err
		}
		return &res, nil
	}
	return nil, fmt.Errorf("unknown kind %q", r.Kind)
}

// persistWizardSelectionV2 writes a kind-aware .fdh/manifest.yaml +
// .fdh/lock.yaml + ~/.fdh/state.json entry for the wizard's choices.
// Replaces persistWizardSelection: the v1 version only knew skills.
//
// The harness name is recorded so `fdh switch <other>` (and the
// downstream manifest-flow `fdh install`) sees a coherent declarative
// state to diff against.
func persistWizardSelectionV2(
	rc *runContext,
	reg *hubregistry.Registry,
	harnessName string,
	refs []componentRef,
	installedByFDH string,
) error {
	_ = installedByFDH
	m := &consumermanifest.Manifest{
		SchemaVersion: 1,
		Harness:       harnessName,
	}
	for _, r := range refs {
		ent := consumermanifest.Entry{Name: r.Name}
		switch r.Kind {
		case managed.KindSkill:
			m.Skills = append(m.Skills, ent)
		case managed.KindRule:
			m.Rules = append(m.Rules, ent)
		case managed.KindAgent:
			m.Agents = append(m.Agents, ent)
		case managed.KindHook:
			m.Hooks = append(m.Hooks, ent)
		}
	}
	if err := consumermanifest.Write(rc.ProjectRoot, m); err != nil {
		return err
	}
	// Harness lookup for Expand. Mirrors install_manifest.go's
	// harnessLookup wiring so the manifest the wizard wrote round-trips
	// cleanly through the resolver.
	harnessLookup := func(name string) ([]consumermanifest.HarnessMember, error) {
		doc, herr := reg.LoadHarnesses()
		if herr != nil {
			return nil, herr
		}
		h, ok := doc.Harnesses[name]
		if !ok {
			return nil, fmt.Errorf("harness %q not in hub", name)
		}
		out := []consumermanifest.HarnessMember{}
		for _, n := range h.Skills {
			out = append(out, consumermanifest.HarnessMember{Name: n, Kind: managed.KindSkill})
		}
		for _, n := range h.Rules {
			out = append(out, consumermanifest.HarnessMember{Name: n, Kind: managed.KindRule})
		}
		for _, n := range h.Agents {
			out = append(out, consumermanifest.HarnessMember{Name: n, Kind: managed.KindAgent})
		}
		for _, n := range h.Hooks {
			out = append(out, consumermanifest.HarnessMember{Name: n, Kind: managed.KindHook})
		}
		return out, nil
	}
	resolved, err := consumermanifest.Expand(m, reg, harnessLookup)
	if err != nil {
		return err
	}
	lock := consumerlock.Build(resolved, reg.HubCommit, timeNow(), harnessName)
	if err := consumerlock.Write(rc.ProjectRoot, lock); err != nil {
		return err
	}
	managedPaths := collectManagedPathsForGitignore(rc.ProjectRoot, nil)
	_ = gitignore.Apply(rc.ProjectRoot, managedPaths)
	if rc.HomeDir != "" {
		if s, sErr := state.Load(rc.HomeDir); sErr == nil {
			s.UpsertProject(rc.ProjectRoot, state.ProjectEntry{ManagedPaths: managedPaths})
			s.HubCache = state.HubCache{Commit: reg.HubCommit}
			_ = state.Save(rc.HomeDir, s)
		}
	}
	return nil
}
