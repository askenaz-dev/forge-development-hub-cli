package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"

	"github.com/forge/fdh/pkg/adapters"
	"github.com/forge/fdh/pkg/hubregistry"
)

// wizardInput is the data flowing from `runInitWizard` into the
// install loop. Populated either by the TUI (real or mock) or by
// flag parsing in non-interactive mode.
//
// Field shape evolution: Harness/Components were added when the wizard
// learned to ask for a harness as Step 0 and to install across all
// four kinds (skill/rule/agent/hook) rather than skill-only. Legacy
// Skills (and Agents) remain populated by --skills/--agents flags
// from `fdh init` for backward compatibility; the wizard merges them
// into Components/Agents internally.
type wizardInput struct {
	Harness    string         // harness id to base the bundle on; "" = no preselect
	Agents     []string       // agent IDs to install to (claude-code|codex|copilot|opencode)
	Skills     []string       // legacy: skill names (folded into Components[kind=skill])
	Components []componentRef // explicit (name, kind) selection (overrides harness preselect)
}

// componentRef identifies a single hub component (name, kind) the
// wizard or a downstream switch operation must materialize.
type componentRef struct {
	Name string
	Kind string
}

// componentChoice is one offered selection in the wizard's Step 2
// multi-select, kind-aware (replaces the older skill-only shape).
// Default reflects whether the catalog flagged this component with
// `default: true`. FromHarness indicates membership in the harness
// picked at Step 0 — surfaces as a "(in harness)" label suffix and
// drives pre-selection.
type componentChoice struct {
	Name        string
	Kind        string
	Description string
	Default     bool
	FromHarness bool
}

// harnessChoice is one offered option in Step 0's single-select.
type harnessChoice struct {
	Name        string
	Description string
}

// skillChoice is the deprecated single-kind shape kept here so the
// legacy SelectSkills prompter signature continues to compile while
// callers migrate. Internal: the new code path uses componentChoice.
//
// Deprecated: use componentChoice and SelectComponents.
type skillChoice struct {
	Name        string
	Description string
	Default     bool
}

// wizardPrompter is the interface the wizard calls. Production uses
// `huhPrompter`; tests substitute a deterministic fake. The split
// is what makes the wizard testable without driving real TUI input.
//
// SelectHarness runs Step 0: a single-select. When `choices` is empty
// the wizard skips this step entirely. defaultPick is preselected
// when present in choices.
//
// SelectComponents runs Step 2: kind-aware multi-select that lists all
// 4 kinds (skill/rule/agent/hook) with the harness members + catalog
// defaults pre-checked. Returns one componentRef per surviving check.
//
// SelectAgents / SelectSkills / Confirm retain their original
// semantics for the migration window.
type wizardPrompter interface {
	SelectHarness(choices []harnessChoice, defaultPick string) (string, error)
	SelectAgents(detected []string) ([]string, error)
	SelectComponents(choices []componentChoice, preSelected []componentRef) ([]componentRef, error)
	SelectSkills(defaults []skillChoice, extras []skillChoice, preSelected []string) ([]string, error)
	Confirm(summary string) (bool, error)
}

// huhPrompter is the production implementation backed by
// charmbracelet/huh. Lives in this same file to keep the import
// graph small.
type huhPrompter struct {
	in  io.Reader
	out io.Writer
}

// SelectHarness runs Step 0: the harness picker. Single-select
// (a harness is a curated bundle; `harness:` takes one name per the
// consumer-manifest contract — composition is via `extends:`, not
// stacking multiple harnesses). Returns "" if `choices` is empty so
// the caller can skip the step.
func (p huhPrompter) SelectHarness(choices []harnessChoice, defaultPick string) (string, error) {
	if len(choices) == 0 {
		return "", nil
	}
	opts := make([]huh.Option[string], 0, len(choices))
	for _, h := range choices {
		label := h.Name
		if h.Description != "" {
			label = label + "  — " + h.Description
		}
		opts = append(opts, huh.NewOption(label, h.Name))
	}
	picked := defaultPick
	err := huh.NewSelect[string]().
		Title("Which harness should fdh install?").
		Description("A harness pre-configures a bundle of components; you can add/remove individual ones in the next step.").
		Options(opts...).
		Value(&picked).
		Run()
	if err != nil {
		return "", err
	}
	return picked, nil
}

func (p huhPrompter) SelectAgents(detected []string) ([]string, error) {
	if len(detected) == 0 {
		return nil, nil
	}
	opts := make([]huh.Option[string], 0, len(detected))
	for _, id := range detected {
		opts = append(opts, huh.NewOption(id, id))
	}
	var picked []string
	err := huh.NewMultiSelect[string]().
		Title("Which agents should fdh install to?").
		Description("Detected on this machine; multi-select with space.").
		Options(opts...).
		Value(&picked).
		Run()
	if err != nil {
		return nil, err
	}
	return picked, nil
}

// SelectComponents runs Step 2 of the kind-aware wizard. One combined
// multi-select across all four kinds; entries already in the chosen
// harness are pre-checked. Labels carry kind + (in harness) / (default)
// markers so the user can tell at a glance what they're toggling.
func (p huhPrompter) SelectComponents(choices []componentChoice, preSelected []componentRef) ([]componentRef, error) {
	if len(choices) == 0 {
		return nil, nil
	}
	// Cobra/huh's MultiSelect operates on string values; encode the
	// componentRef as "<kind>:<name>" so the round-trip is unambiguous
	// (rules and skills could collide on name otherwise).
	opts := make([]huh.Option[string], 0, len(choices))
	for _, c := range choices {
		label := c.Kind + ": " + c.Name
		marks := []string{}
		if c.FromHarness {
			marks = append(marks, "in harness")
		}
		if c.Default {
			marks = append(marks, "default")
		}
		if len(marks) > 0 {
			label = label + " (" + strings.Join(marks, ", ") + ")"
		}
		if c.Description != "" {
			// Trim the description so the option fits on common terminal widths.
			desc := c.Description
			if len(desc) > 80 {
				desc = desc[:77] + "…"
			}
			label = label + "  — " + desc
		}
		opts = append(opts, huh.NewOption(label, c.Kind+":"+c.Name))
	}
	picked := make([]string, 0, len(preSelected))
	for _, r := range preSelected {
		picked = append(picked, r.Kind+":"+r.Name)
	}
	err := huh.NewMultiSelect[string]().
		Title("Which components do you want?").
		Description("Pre-checked items are members of the harness you picked. Uncheck to opt out; check extras to opt in.").
		Options(opts...).
		Value(&picked).
		Run()
	if err != nil {
		return nil, err
	}
	out := make([]componentRef, 0, len(picked))
	for _, v := range picked {
		kind, name, ok := strings.Cut(v, ":")
		if !ok {
			continue
		}
		out = append(out, componentRef{Name: name, Kind: kind})
	}
	return out, nil
}

func (p huhPrompter) SelectSkills(defaults []skillChoice, extras []skillChoice, preSelected []string) ([]string, error) {
	all := append([]skillChoice(nil), defaults...)
	all = append(all, extras...)
	if len(all) == 0 {
		return nil, nil
	}
	opts := make([]huh.Option[string], 0, len(all))
	for _, s := range all {
		label := s.Name
		if s.Default {
			label = label + " (default)"
		}
		if s.Description != "" {
			label = label + "  — " + s.Description
		}
		opts = append(opts, huh.NewOption(label, s.Name))
	}
	picked := append([]string(nil), preSelected...)
	err := huh.NewMultiSelect[string]().
		Title("Which skills do you want?").
		Description("Defaults are pre-checked. Uncheck to opt out; check extras to opt in.").
		Options(opts...).
		Value(&picked).
		Run()
	if err != nil {
		return nil, err
	}
	return picked, nil
}

func (p huhPrompter) Confirm(summary string) (bool, error) {
	var ok bool
	err := huh.NewConfirm().
		Title("Proceed with these selections?").
		Description(summary).
		Affirmative("Install").
		Negative("Cancel").
		Value(&ok).
		Run()
	if err != nil {
		return false, err
	}
	return ok, nil
}

// stdinIsTTY reports whether stdin is attached to an interactive
// terminal. Used by runInit to pick wizard vs non-interactive mode.
func stdinIsTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// runInitWizard owns Steps 1-3 of the wizard plus the install loop.
// Decoupled from runInit (which still does config writing + doctor)
// so tests can drive it with a fake prompter and a fixture hub.
//
// Returns `selected_agents`, `selected_skills`, `installed_skills`
// for inclusion in the JSON result.
//
// The non-interactive code path (flags-only, no prompter) takes the
// same code path with `wizardInput` pre-populated; that keeps the
// install loop the single source of truth.
func runInitWizard(
	ctx context.Context,
	stdout io.Writer,
	stderr io.Writer,
	rc *runContext,
	hubURL string,
	prompter wizardPrompter,
	input wizardInput,
	noDefaults bool,
	dryRun bool,
	installedByFDH string,
) ([]string, []string, []InstalledSkillResult, error) {
	reg, err := hubregistry.Load(ctx, hubURL, hubregistry.LoadOptions{
		Logger: func(line string) {
			if rc.Verbose {
				fmt.Fprintln(stderr, "[hub] "+line)
			}
		},
	})
	if err != nil {
		var corrupt *hubregistry.CorruptCacheError
		if errors.As(err, &corrupt) {
			fmt.Fprintf(stderr, "warning: hub cache at %s was corrupt; re-cloning…\n", corrupt.CacheDir)
			if err := hubregistry.RecoverFromCorruption(corrupt.CacheDir); err != nil {
				return nil, nil, nil, Wrap(ExitGenericFailure, fmt.Errorf("recover hub cache: %w", err))
			}
			reg, err = hubregistry.Load(ctx, hubURL, hubregistry.LoadOptions{})
		}
		if err != nil {
			return nil, nil, nil, Wrap(ExitRegistryUnreach, fmt.Errorf("load hub: %w", err))
		}
	}

	// Step 0: harness. Optional — skipped silently when the hub
	// publishes no harnesses (empty hub/harnesses.yaml) or when the
	// caller already passed --harness via the wizardInput.
	harnessesDoc, hErr := reg.LoadHarnesses()
	if hErr != nil {
		return nil, nil, nil, Wrap(ExitGenericFailure,
			fmt.Errorf("load hub harnesses: %w", hErr))
	}
	harnessName := input.Harness
	if prompter != nil && harnessName == "" && len(harnessesDoc.Harnesses) > 0 {
		choices := harnessChoicesFromDoc(harnessesDoc)
		defaultPick := defaultHarnessName(harnessesDoc)
		harnessName, err = prompter.SelectHarness(choices, defaultPick)
		if err != nil {
			return nil, nil, nil, Wrap(ExitGenericFailure, err)
		}
	} else if harnessName == "" && len(harnessesDoc.Harnesses) > 0 {
		// Non-interactive default: use "default" if it exists.
		if _, ok := harnessesDoc.Harnesses["default"]; ok {
			harnessName = "default"
		}
	}
	if harnessName != "" {
		if _, ok := harnessesDoc.Harnesses[harnessName]; !ok {
			avail := make([]string, 0, len(harnessesDoc.Harnesses))
			for n := range harnessesDoc.Harnesses {
				avail = append(avail, n)
			}
			sort.Strings(avail)
			return nil, nil, nil, Errorf(ExitInvalidUsage,
				"unknown harness %q (available: %s)", harnessName, strings.Join(avail, ", "))
		}
	}

	// Step 1: agents.
	detected := detectedWizardAgents(rc)
	if len(detected) == 0 {
		return nil, nil, nil, Errorf(ExitNoAgent, "no AI agents detected on this host — install at least one (Claude Code, Codex, Copilot, OpenCode) and re-run init")
	}

	agents := input.Agents
	if prompter != nil && len(agents) == 0 {
		agents, err = prompter.SelectAgents(detected)
		if err != nil {
			return nil, nil, nil, Wrap(ExitGenericFailure, err)
		}
	} else if len(agents) == 0 {
		// Non-interactive default: install to every detected agent.
		agents = detected
	}
	agents = intersectKnown(agents, detected)
	if len(agents) == 0 {
		return nil, nil, nil, Errorf(ExitNoAgent, "no agents selected; either pass --agents or run interactively")
	}

	// Step 2: components (kind-aware). The choice set is every
	// catalog entry whose `agents_supported` overlaps the chosen
	// agents; the pre-select is the harness members PLUS catalog
	// defaults (when --no-defaults isn't passed).
	harnessMembers := harnessMemberRefs(harnessesDoc, harnessName)
	choices, preSelect := buildComponentChoices(reg, agents, harnessMembers, noDefaults)

	// Legacy --skills flag injects into Components (kind=skill) so old
	// non-interactive calls keep working.
	wantedComponents := append([]componentRef(nil), input.Components...)
	for _, s := range input.Skills {
		wantedComponents = append(wantedComponents, componentRef{Name: s, Kind: hubregistry.KindSkill})
	}

	if prompter != nil && len(wantedComponents) == 0 {
		wantedComponents, err = prompter.SelectComponents(choices, preSelect)
		if err != nil {
			return nil, nil, nil, Wrap(ExitGenericFailure, err)
		}
	} else if len(wantedComponents) == 0 {
		// Non-interactive default: the pre-selected set (harness +
		// defaults). This is the headless equivalent of "user accepted
		// the wizard's defaults".
		wantedComponents = append([]componentRef(nil), preSelect...)
	}

	resolved, unknown := resolveComponentSelection(wantedComponents, reg)
	if len(unknown) > 0 {
		names := make([]string, 0, len(unknown))
		for _, u := range unknown {
			names = append(names, u.Kind+":"+u.Name)
		}
		return nil, nil, nil, Errorf(ExitInvalidUsage,
			"unknown component(s): %s", strings.Join(names, ", "))
	}

	// Skills view for the legacy return tuple — drops everything that
	// isn't kind=skill so existing callers/CI scripts that key off
	// `selected_skills` keep matching.
	skills := skillNamesFromRefs(resolved)

	// Step 3: confirmation.
	if prompter != nil {
		summary := summariseComponentSelection(harnessName, agents, resolved, dryRun)
		ok, err := prompter.Confirm(summary)
		if err != nil {
			return nil, nil, nil, Wrap(ExitGenericFailure, err)
		}
		if !ok {
			fmt.Fprintln(stdout, "Canceled.")
			return agents, skills, nil, nil
		}
	}

	// Install loop — kind-aware, routes each component to the right
	// adapter family. Mirrors the routing in install_manifest.go's
	// installResolvedComponent (kept here rather than imported because
	// the wizard owns its own InstallOpts/dryRun semantics).
	scope, err := resolveScope("auto", rc)
	if err != nil {
		return agents, skills, nil, err
	}
	installed, installErr := installResolvedRefs(
		ctx, reg, rc, scope, resolved, agents,
		installedByFDH, dryRun,
	)
	if installErr != nil {
		return agents, skills, installed, installErr
	}

	// Persist manifest + lock + state for the chosen selection so
	// `fdh install` / `fdh update` / `fdh switch` see a coherent
	// declarative state across all four kinds.
	if rc.ProjectRoot != "" && len(resolved) > 0 {
		_ = persistWizardSelectionV2(rc, reg, harnessName, resolved, installedByFDH)
	}
	return agents, skills, installed, nil
}

// timeNow is a small indirection so tests can stub if needed (none do yet).
func timeNow() time.Time { return time.Now() }

// detectedWizardAgents returns the IDs of agents present on this host
// for whom a SkillAdapter is shipped. The intersection guards against
// "detected agent we can't install to" gaps that would otherwise
// show up to the user as a confusing option in the wizard.
func detectedWizardAgents(rc *runContext) []string {
	var out []string
	for _, id := range detectedAgentIDs(rc) {
		if adapters.SkillAdapterByID(id) != nil {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// splitDefaultsAndExtras partitions the catalog into the wizard's
// two MultiSelect groups, retaining only entries that support at
// least one of the chosen agents.
func splitDefaultsAndExtras(all []hubregistry.ComponentEntry, agents []string) (defaults, extras []skillChoice) {
	for _, s := range all {
		if !anyShared(s.AgentsSupported, agents) {
			continue
		}
		c := skillChoice{Name: s.Name, Description: s.Description, Default: s.Default}
		if s.Default {
			defaults = append(defaults, c)
		} else {
			extras = append(extras, c)
		}
	}
	sort.Slice(defaults, func(i, j int) bool { return defaults[i].Name < defaults[j].Name })
	sort.Slice(extras, func(i, j int) bool { return extras[i].Name < extras[j].Name })
	return
}

// resolveSkillSelection maps the user-given names to the registry,
// returning the validated list and any unknown names so the caller
// can surface a clear error.
func resolveSkillSelection(picked []string, catalog []hubregistry.ComponentEntry) (valid, unknown []string) {
	known := map[string]bool{}
	for _, e := range catalog {
		known[e.Name] = true
	}
	for _, p := range picked {
		// Honor `+name` / `-name` shorthand from the spec
		// fdh-init-interactive: `+` adds, `-` removes. The CLI keeps
		// the simple form here (presence ⇒ install); `-` filtering is
		// handled by the caller by setting --no-defaults.
		p = strings.TrimPrefix(p, "+")
		if known[p] {
			valid = append(valid, p)
		} else {
			unknown = append(unknown, p)
		}
	}
	return
}

func intersectKnown(want, known []string) []string {
	out := make([]string, 0, len(want))
	for _, w := range want {
		if contains(known, w) {
			out = append(out, w)
		}
	}
	return out
}

func anyShared(a, b []string) bool {
	for _, x := range a {
		if contains(b, x) {
			return true
		}
	}
	return false
}

// InstalledSkillResult is the per-(skill,agent) record placed in the
// `installed_skills` slice of InitResult / UpdateResult. The shape is
// stable: new fields may be appended but existing ones SHALL NOT
// change (additive-only contract).
type InstalledSkillResult struct {
	Skill       string   `json:"skill"`
	Agent       string   `json:"agent"`
	TargetPath  string   `json:"target_path"`
	MarkerPath  string   `json:"marker_path"`
	ContentHash string   `json:"content_hash"`
	Skipped     bool     `json:"skipped,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

// hubURLFromConfigOrFlag picks the hub git URL the wizard should
// load. Order: explicit registry.url, then existing config (which
// runInit already populated). Returns "" when nothing is set.
func hubURLFromConfigOrFlag(rc *runContext) string {
	if rc == nil || rc.Registry == nil {
		return ""
	}
	// rc.Registry.Source() returns "git:<url> (clone at …)" or
	// "git:<localpath>". Extract the URL prefix if present.
	src := rc.Registry.Source()
	const prefix = "git:"
	if !strings.HasPrefix(src, prefix) {
		return ""
	}
	src = strings.TrimPrefix(src, prefix)
	if i := strings.Index(src, " "); i >= 0 {
		src = src[:i]
	}
	// If the resolved URL is a local path (no scheme, no ".git"),
	// it's a developer-pre-populated registry — still a valid hub.
	return src
}

// reExportForTests is a no-op symbol that keeps filepath imported
// when conditional code paths above are pruned at build time.
var _ = filepath.Separator
