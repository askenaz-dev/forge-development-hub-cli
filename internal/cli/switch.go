package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/forge/fdh/pkg/adapters"
	"github.com/forge/fdh/pkg/consumerlock"
	"github.com/forge/fdh/pkg/consumermanifest"
	"github.com/forge/fdh/pkg/gitignore"
	"github.com/forge/fdh/pkg/hubregistry"
	"github.com/forge/fdh/pkg/managed"
	"github.com/forge/fdh/pkg/state"
)

// SwitchResult is the JSON shape emitted by `fdh switch --json`.
//
// The fields are deliberately separated by lifecycle stage:
//
//	From / To              describe the manifest-level transition.
//	Installed              new components materialized.
//	Uninstalled            components no longer in the harness, removed
//	                       from every agent install dir.
//	Unchanged              components present in both harnesses.
//	LockWritten            the new lockfile reflects the new bundle.
//	GitignoreUpdated       the .gitignore managed block was rewritten.
//
// Additive-only contract: new fields may be appended; nothing here is
// renamed or repurposed across releases.
type SwitchResult struct {
	From             string         `json:"from"`
	To               string         `json:"to"`
	DryRun           bool           `json:"dry_run,omitempty"`
	Installed        []SwitchChange `json:"installed,omitempty"`
	Uninstalled      []SwitchChange `json:"uninstalled,omitempty"`
	Unchanged        []SwitchChange `json:"unchanged,omitempty"`
	LockWritten      bool           `json:"lock_written"`
	GitignoreUpdated bool           `json:"gitignore_updated"`
}

// SwitchChange is one (kind, name) pair affected by the switch.
type SwitchChange struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

func newSwitchCmd(info BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "switch <harness>",
		Short: "Switch the project to a different harness, materializing the diff",
		Long: `Set this project's manifest to a different harness and reconcile the
filesystem against it: install components added by the new harness,
uninstall components dropped by it, keep the rest. The .fdh/manifest.yaml,
.fdh/lock.yaml, .gitignore managed block, and ~/.fdh/state.json are
updated in one transaction.

A project root with .fdh/manifest.yaml is required. Run 'fdh init' first
to create one if you haven't already.

Examples:
  fdh switch backend-team
  fdh switch frontend-team --dry-run
  fdh switch default --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSwitch(cmd, args, info)
		},
	}
	cmd.Flags().Bool("dry-run", false, "compute the diff without modifying anything")
	return cmd
}

func runSwitch(cmd *cobra.Command, args []string, info BuildInfo) error {
	target := args[0]
	verbose, _ := cmd.PersistentFlags().GetBool("verbose")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	rc, err := buildRunContext(ctx, info, verbose)
	if err != nil {
		return err
	}
	if rc.ProjectRoot == "" {
		return Errorf(ExitInvalidUsage,
			"fdh switch requires a project root (run inside a directory with a .git/ folder)")
	}

	// Load the existing manifest. Switch needs an existing project —
	// it's a transition, not an init.
	manifest, mErr := consumermanifest.Load(rc.ProjectRoot)
	if mErr != nil {
		if os.IsNotExist(mErr) {
			return Errorf(ExitInvalidUsage,
				"no %s in %s — run `fdh init` first",
				consumermanifest.Filename, rc.ProjectRoot)
		}
		return Wrap(ExitInvalidUsage, mErr)
	}
	for _, w := range manifest.Warnings {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+w)
	}
	if err := consumermanifest.Validate(manifest); err != nil {
		return Wrap(ExitInvalidUsage, err)
	}

	// Load the hub. The CLI's standard registry transport gives us a
	// hubregistry.Registry whose LoadHarnesses + Components view we
	// need to resolve the new harness's membership.
	hubURL := hubURLFromConfigOrFlag(rc)
	if hubURL == "" {
		return Errorf(ExitInvalidUsage,
			"no registry URL configured — set registry.url or run `fdh init` first")
	}
	reg, err := hubregistry.Load(ctx, hubURL, hubregistry.LoadOptions{
		Logger: func(line string) {
			if rc.Verbose {
				fmt.Fprintln(cmd.ErrOrStderr(), "[hub] "+line)
			}
		},
	})
	if err != nil {
		var corrupt *hubregistry.CorruptCacheError
		if errors.As(err, &corrupt) {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"warning: hub cache at %s was corrupt; re-cloning…\n", corrupt.CacheDir)
			if rerr := hubregistry.RecoverFromCorruption(corrupt.CacheDir); rerr != nil {
				return Wrap(ExitGenericFailure, fmt.Errorf("recover hub cache: %w", rerr))
			}
			reg, err = hubregistry.Load(ctx, hubURL, hubregistry.LoadOptions{})
		}
		if err != nil {
			return Wrap(ExitRegistryUnreach, fmt.Errorf("load hub: %w", err))
		}
	}

	harnesses, err := reg.LoadHarnesses()
	if err != nil {
		return Wrap(ExitGenericFailure, fmt.Errorf("load harnesses.yaml: %w", err))
	}
	if _, ok := harnesses.Harnesses[target]; !ok {
		names := make([]string, 0, len(harnesses.Harnesses))
		for n := range harnesses.Harnesses {
			names = append(names, n)
		}
		sort.Strings(names)
		return Errorf(ExitInvalidUsage,
			"unknown harness %q (available: %v)", target, names)
	}

	result := SwitchResult{
		From:   manifest.Harness,
		To:     target,
		DryRun: dryRun,
	}

	// Compute the OLD resolution (current state on disk) and the NEW
	// resolution (what disk should look like after switch). Diff them
	// by (name, kind) to decide install/uninstall/unchanged.
	oldRefs, err := resolveCurrentManifest(manifest, reg)
	if err != nil {
		return Wrap(ExitInvalidUsage, fmt.Errorf("resolve current manifest: %w", err))
	}

	// Build the target manifest: keep the user's extras / explicit
	// entries; swap only the harness name. This preserves `extends:`
	// add/remove modifiers across the switch — the user's intent
	// stays sticky.
	targetManifest := *manifest
	targetManifest.Harness = target
	newRefs, err := resolveCurrentManifest(&targetManifest, reg)
	if err != nil {
		return Wrap(ExitInvalidUsage, fmt.Errorf("resolve target manifest: %w", err))
	}

	installList, uninstallList, unchanged := diffComponentSets(oldRefs, newRefs)
	for _, c := range installList {
		result.Installed = append(result.Installed, SwitchChange{Kind: c.Kind, Name: c.Name})
	}
	for _, c := range uninstallList {
		result.Uninstalled = append(result.Uninstalled, SwitchChange{Kind: c.Kind, Name: c.Name})
	}
	for _, c := range unchanged {
		result.Unchanged = append(result.Unchanged, SwitchChange{Kind: c.Kind, Name: c.Name})
	}

	if dryRun {
		// Sort for stable output before emit.
		sortChanges(result.Installed)
		sortChanges(result.Uninstalled)
		sortChanges(result.Unchanged)
		if outputMode(cmd) == "json" {
			return emitJSON(cmd.OutOrStdout(), result)
		}
		return printSwitchTable(cmd.OutOrStdout(), result)
	}

	// 1) Uninstall: remove from every agent install dir.
	scope, err := resolveScope("auto", rc)
	if err != nil {
		return err
	}
	for _, c := range uninstallList {
		if uErr := uninstallOneByName(rc, scope, c.Name); uErr != nil {
			return Wrap(ExitGenericFailure,
				fmt.Errorf("uninstall %s/%s: %w", c.Kind, c.Name, uErr))
		}
	}

	// 2) Install: route each new ref to its adapter.
	detected := detectedAgentIDs(rc)
	if _, err = installResolvedRefs(
		ctx, reg, rc, scope, installList, detected, info.Version, false,
	); err != nil {
		return err
	}

	// 3) Persist the new manifest.
	if err := consumermanifest.Write(rc.ProjectRoot, &targetManifest); err != nil {
		return Wrap(ExitGenericFailure, fmt.Errorf("write manifest: %w", err))
	}

	// 4) Rebuild lock from the new resolution.
	resolvedForLock, err := expandManifestForReg(&targetManifest, reg)
	if err != nil {
		return Wrap(ExitInvalidUsage, fmt.Errorf("expand for lock: %w", err))
	}
	lock := consumerlock.Build(resolvedForLock, reg.HubCommit, time.Now(), target)
	if err := consumerlock.Write(rc.ProjectRoot, lock); err != nil {
		return Wrap(ExitGenericFailure, err)
	}
	result.LockWritten = true

	// 5) Update .gitignore managed block.
	managedPaths := collectManagedPathsForGitignore(rc.ProjectRoot, nil)
	if err := gitignore.Apply(rc.ProjectRoot, managedPaths); err != nil {
		return Wrap(ExitGenericFailure, fmt.Errorf("update .gitignore: %w", err))
	}
	result.GitignoreUpdated = true

	// 6) Update the per-machine state ledger.
	if rc.HomeDir != "" {
		if s, sErr := state.Load(rc.HomeDir); sErr == nil {
			s.UpsertProject(rc.ProjectRoot, state.ProjectEntry{ManagedPaths: managedPaths})
			s.HubCache = state.HubCache{
				LastPulled: time.Now().UTC(),
				Commit:     reg.HubCommit,
				URL:        hubURL,
			}
			_ = state.Save(rc.HomeDir, s)
		}
	}

	sortChanges(result.Installed)
	sortChanges(result.Uninstalled)
	sortChanges(result.Unchanged)
	if outputMode(cmd) == "json" {
		return emitJSON(cmd.OutOrStdout(), result)
	}
	return printSwitchTable(cmd.OutOrStdout(), result)
}

// resolveCurrentManifest runs Expand with a harness lookup wired to
// the in-memory registry — mirrors install_manifest.go's setup.
// Returns componentRef slice for diffing.
func resolveCurrentManifest(
	m *consumermanifest.Manifest,
	reg *hubregistry.Registry,
) ([]componentRef, error) {
	resolved, err := expandManifestForReg(m, reg)
	if err != nil {
		return nil, err
	}
	out := make([]componentRef, 0, len(resolved))
	for _, r := range resolved {
		out = append(out, componentRef{Name: r.Name, Kind: r.Kind})
	}
	return out, nil
}

// expandManifestForReg wraps consumermanifest.Expand with a harness
// lookup backed by the in-memory hub registry. Mirrors the wiring in
// install_manifest.go (kept here to avoid an awkward shared helper
// during the migration window — the duplication is intentional).
func expandManifestForReg(
	m *consumermanifest.Manifest,
	reg *hubregistry.Registry,
) ([]consumermanifest.ResolvedComponent, error) {
	lookup := func(name string) ([]consumermanifest.HarnessMember, error) {
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
	return consumermanifest.Expand(m, reg, lookup)
}

// diffComponentSets returns the set differences needed by switch:
//
//	install   = new \ old
//	uninstall = old \ new
//	unchanged = old ∩ new
func diffComponentSets(oldRefs, newRefs []componentRef) (install, uninstall, unchanged []componentRef) {
	oldSet := map[componentRef]bool{}
	for _, r := range oldRefs {
		oldSet[r] = true
	}
	newSet := map[componentRef]bool{}
	for _, r := range newRefs {
		newSet[r] = true
	}
	for _, r := range newRefs {
		if oldSet[r] {
			unchanged = append(unchanged, r)
		} else {
			install = append(install, r)
		}
	}
	for _, r := range oldRefs {
		if !newSet[r] {
			uninstall = append(uninstall, r)
		}
	}
	return
}

// switchCandidate is one (path, marker) pair the switch command may
// remove during the uninstall phase. Local to switch.go to avoid
// muddying the existing uninstallCandidate shape in uninstall.go.
type switchCandidate struct {
	removePath string
	markerPath string
}

// uninstallOneByName runs the same find-and-remove logic as
// `fdh uninstall <name>` but scoped to one name. Walks every
// skill/rule/agent install dir of every detected agent at the given
// scope, removes matched markers + their content sibling.
//
// Today's findUninstallCandidates only walks SkillAdapters; this
// helper extends to the other three kinds inline. A future refactor
// could promote it back into uninstall.go.
func uninstallOneByName(rc *runContext, scope adapters.Scope, name string) error {
	var all []switchCandidate
	for _, a := range adapters.AllSkillAdapters() {
		root, err := adapterScopeRoot(a, rc.HomeDir, rc.ProjectRoot, scope)
		if err != nil {
			continue
		}
		all = append(all, collectMatchingCandidates(root, name)...)
	}
	for _, a := range adapters.AllRuleAdapters() {
		root, err := adapterScopeRootForRule(a, rc.HomeDir, rc.ProjectRoot, scope)
		if err != nil {
			continue
		}
		all = append(all, collectMatchingCandidates(root, name)...)
	}
	for _, a := range adapters.AllAgentAdapters() {
		root, err := adapterScopeRootForAgent(a, rc.HomeDir, rc.ProjectRoot, scope)
		if err != nil {
			continue
		}
		all = append(all, collectMatchingCandidates(root, name)...)
	}
	// Hooks live as edits inside a settings.json managed block, not as
	// removable files. The hook adapter's own uninstall (when we ship
	// it) is the right path; for now we no-op and let `fdh repair`
	// surface drift if a hook needs purging.
	for _, c := range all {
		if err := os.RemoveAll(c.removePath); err != nil {
			return fmt.Errorf("remove %s: %w", c.removePath, err)
		}
		if c.markerPath != "" && c.markerPath != c.removePath {
			_ = os.Remove(c.markerPath)
		}
	}
	return nil
}

// adapterScopeRootForRule mirrors adapterScopeRoot for the
// RuleAdapter family. (The existing helper is SkillAdapter-typed; a
// small reflection-free duplication is the cheapest path here.)
func adapterScopeRootForRule(a adapters.RuleAdapter, homeDir, projectRoot string, scope adapters.Scope) (string, error) {
	tp, err := a.TargetPath("", projectRoot, homeDir, scope)
	if err != nil {
		return "", err
	}
	return tp, nil
}

// adapterScopeRootForAgent mirrors adapterScopeRoot for AgentAdapter.
func adapterScopeRootForAgent(a adapters.AgentAdapter, homeDir, projectRoot string, scope adapters.Scope) (string, error) {
	tp, err := a.TargetPath("", projectRoot, homeDir, scope)
	if err != nil {
		return "", err
	}
	return tp, nil
}

// collectMatchingCandidates walks one install root and finds the
// component whose marker matches `name`. Caps at the immediate
// children of root so we don't recurse into bundle contents
// accidentally.
func collectMatchingCandidates(root, name string) []switchCandidate {
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []switchCandidate
	for _, e := range entries {
		full := root + string(os.PathSeparator) + e.Name()
		if e.IsDir() {
			markerPath := full + string(os.PathSeparator) + managed.Filename
			m, mErr := managed.Read(markerPath)
			if mErr != nil {
				continue
			}
			if m.Name != name {
				continue
			}
			out = append(out, switchCandidate{removePath: full, markerPath: markerPath})
		}
	}
	return out
}

func sortChanges(s []SwitchChange) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Kind != s[j].Kind {
			return s[i].Kind < s[j].Kind
		}
		return s[i].Name < s[j].Name
	})
}

func printSwitchTable(w io.Writer, r SwitchResult) error {
	prefix := ""
	if r.DryRun {
		prefix = "[dry-run] "
	}
	fmt.Fprintf(w, "%sSwitch: %s → %s\n", prefix, displayFrom(r.From), r.To)
	if len(r.Installed) > 0 {
		fmt.Fprintln(w, "  Installed:")
		for _, c := range r.Installed {
			fmt.Fprintf(w, "    + %s/%s\n", c.Kind, c.Name)
		}
	}
	if len(r.Uninstalled) > 0 {
		fmt.Fprintln(w, "  Uninstalled:")
		for _, c := range r.Uninstalled {
			fmt.Fprintf(w, "    - %s/%s\n", c.Kind, c.Name)
		}
	}
	if len(r.Unchanged) > 0 {
		fmt.Fprintln(w, "  Unchanged:")
		for _, c := range r.Unchanged {
			fmt.Fprintf(w, "    = %s/%s\n", c.Kind, c.Name)
		}
	}
	if !r.DryRun {
		fmt.Fprintf(w, "  Manifest + lock + .gitignore updated.\n")
	}
	return nil
}

func displayFrom(s string) string {
	if s == "" {
		return "(no harness)"
	}
	return s
}
