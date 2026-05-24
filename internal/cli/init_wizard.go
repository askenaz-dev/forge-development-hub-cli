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

	"github.com/charmbracelet/huh"
	"github.com/forge/fdh/pkg/adapters"
	"github.com/forge/fdh/pkg/hubregistry"
	"golang.org/x/term"
)

// wizardInput is the data flowing from `runInitWizard` into the
// install loop. Populated either by the TUI (real or mock) or by
// flag parsing in non-interactive mode.
type wizardInput struct {
	Agents []string // agent IDs to install to
	Skills []string // skill names to install
}

// skillChoice is one offered selection in Step 2 of the wizard.
type skillChoice struct {
	Name        string
	Description string
	Default     bool
}

// wizardPrompter is the interface the wizard calls. Production uses
// `huhPrompter`; tests substitute a deterministic fake. The split
// is what makes the wizard testable without driving real TUI input.
type wizardPrompter interface {
	SelectAgents(detected []string) ([]string, error)
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
		Title("Which agents should fdh install skills to?").
		Description("Detected on this machine; multi-select with space.").
		Options(opts...).
		Value(&picked).
		Run()
	if err != nil {
		return nil, err
	}
	return picked, nil
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

	// Step 2: skills. Filter the hub catalog by the chosen agents.
	defaults, extras := splitDefaultsAndExtras(reg.Skills, agents)
	if noDefaults {
		// User opted out of defaults entirely.
		defaults = nil
	}

	skills := input.Skills
	if prompter != nil && len(skills) == 0 {
		preSelected := skillNames(defaults)
		skills, err = prompter.SelectSkills(defaults, extras, preSelected)
		if err != nil {
			return nil, nil, nil, Wrap(ExitGenericFailure, err)
		}
	} else if len(skills) == 0 {
		// Non-interactive default: install all defaults.
		skills = skillNames(defaults)
	}

	skills, unknown := resolveSkillSelection(skills, reg.Skills)
	if len(unknown) > 0 {
		return nil, nil, nil, Errorf(ExitInvalidUsage,
			"unknown skill(s): %s", strings.Join(unknown, ", "))
	}

	// Step 3: confirmation.
	if prompter != nil {
		summary := summariseSelection(agents, skills, dryRun)
		ok, err := prompter.Confirm(summary)
		if err != nil {
			return nil, nil, nil, Wrap(ExitGenericFailure, err)
		}
		if !ok {
			fmt.Fprintln(stdout, "Cancelled.")
			return agents, skills, nil, nil
		}
	}

	// Install loop.
	scope, err := resolveScope("auto", rc)
	if err != nil {
		return agents, skills, nil, err
	}
	var installed []InstalledSkillResult
	for _, name := range skills {
		entry := reg.SkillByName(name)
		if entry == nil {
			// Should be unreachable — resolveSkillSelection rejects unknowns.
			continue
		}
		srcDir, err := reg.FetchSkill(ctx, name)
		if err != nil {
			return agents, skills, installed, Wrap(ExitRegistryUnreach,
				fmt.Errorf("fetch skill %s: %w", name, err))
		}
		for _, agentID := range agents {
			if !contains(entry.AgentsSupported, agentID) {
				continue
			}
			adapter := adapters.SkillAdapterByID(agentID)
			if adapter == nil {
				return agents, skills, installed, Errorf(ExitNoAgent,
					"agent %q has no installed adapter (programming error)", agentID)
			}
			opts := adapters.InstallOpts{
				SkillName:      name,
				ProjectRoot:    rc.ProjectRoot,
				HomeDir:        rc.HomeDir,
				Scope:          scope,
				HubVersion:     entry.Version,
				HubCommit:      reg.HubCommit,
				InstalledByFDH: installedByFDH,
				DryRun:         dryRun,
			}
			res, err := adapter.Install(srcDir, opts)
			if err != nil {
				return agents, skills, installed, Wrap(ExitGenericFailure,
					fmt.Errorf("install %s for %s: %w", name, agentID, err))
			}
			installed = append(installed, InstalledSkillResult{
				Skill:       name,
				Agent:       agentID,
				TargetPath:  res.TargetPath,
				ContentHash: res.ContentHash,
				MarkerPath:  res.MarkerPath,
				Skipped:     res.Skipped,
				Warnings:    res.Warnings,
			})
		}
	}
	return agents, skills, installed, nil
}

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
func splitDefaultsAndExtras(all []hubregistry.SkillEntry, agents []string) (defaults, extras []skillChoice) {
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

func skillNames(choices []skillChoice) []string {
	out := make([]string, 0, len(choices))
	for _, c := range choices {
		out = append(out, c.Name)
	}
	return out
}

// resolveSkillSelection maps the user-given names to the registry,
// returning the validated list and any unknown names so the caller
// can surface a clear error.
func resolveSkillSelection(picked []string, catalog []hubregistry.SkillEntry) (valid, unknown []string) {
	known := map[string]bool{}
	for _, e := range catalog {
		known[e.Name] = true
	}
	for _, p := range picked {
		// Honour `+name` / `-name` shorthand from the spec
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

func summariseSelection(agents, skills []string, dryRun bool) string {
	var b strings.Builder
	if dryRun {
		b.WriteString("[dry-run] ")
	}
	fmt.Fprintf(&b, "Agents: %s\nSkills: %s",
		strings.Join(agents, ", "),
		strings.Join(skills, ", "))
	return b.String()
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
