package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
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

// ManifestInstallResult is the JSON shape emitted by manifest-flow
// `fdh install` (no positional argument). Distinct from
// InstallResult because the data is per-flow, not per-skill.
type ManifestInstallResult struct {
	FromManifest        bool                `json:"from_manifest"`
	Frozen              bool                `json:"frozen,omitempty"`
	HubCommit           string              `json:"hub_commit,omitempty"`
	ResolvedFrom        string              `json:"resolved_from_profile,omitempty"`
	Components          []ManifestComponent `json:"components"`
	LockWritten         bool                `json:"lock_written"`
	ManifestWritten     bool                `json:"manifest_written,omitempty"`
	GeneratedFromLegacy bool                `json:"generated_from_legacy,omitempty"`
	GitignoreUpdated    bool                `json:"gitignore_updated,omitempty"`
}

// ManifestComponent is one resolved+materialized component entry.
type ManifestComponent struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Version      string   `json:"version,omitempty"`
	HubPath      string   `json:"hub_path"`
	Materialized []string `json:"materialized,omitempty"`
	Skipped      bool     `json:"skipped,omitempty"`
}

// runInstallManifest is the no-args entry point: it resolves the
// consumer's manifest against the hub and materializes the result,
// then writes the lock and updates the gitignore section.
func runInstallManifest(cmd *cobra.Command, rc *runContext, info BuildInfo) error {
	if rc.ProjectRoot == "" {
		return Errorf(ExitInvalidUsage, "no project root detected; manifest-flow install requires a project directory")
	}

	manifest, generatedFromLegacy, err := loadOrGenerateManifest(cmd, rc)
	if err != nil {
		return err
	}
	if manifest == nil {
		// Empty/legacy autogen exited with instructional message; surface success.
		return nil
	}
	if generatedFromLegacy {
		// Spec: generate, instruct, exit 0 without further work.
		return nil
	}

	if err := consumermanifest.Validate(manifest); err != nil {
		return Errorf(ExitInvalidUsage, "%v", err)
	}

	// Profile lookup needs the hub clone — load it before validating.
	// (Profile resolution itself happens below, after we have reg.)

	// Hub catalog access (uses the same path the wizard/update use).
	hubURL := hubURLFromConfigOrFlag(rc)
	if hubURL == "" {
		return Errorf(ExitInvalidUsage, "no hub URL configured (set registry.url or registry.local_path)")
	}
	reg, err := loadHubWithRecovery(rc.Ctx, io.Discard, hubURL, false)
	if err != nil {
		return Wrap(ExitRegistryUnreach, err)
	}

	// Build a profile lookup backed by the hub's profiles.yaml.
	var profileLookup consumermanifest.ProfileLookup
	if manifest.Profile != "" {
		doc, perr := reg.LoadProfiles()
		if perr != nil {
			return Errorf(ExitInvalidUsage, "load profiles: %v", perr)
		}
		profileLookup = func(name string) ([]consumermanifest.ProfileMember, error) {
			p, ok := doc.Profiles[name]
			if !ok {
				return nil, fmt.Errorf("profile %q not found in hub/profiles.yaml", name)
			}
			var out []consumermanifest.ProfileMember
			for _, n := range p.Skills {
				out = append(out, consumermanifest.ProfileMember{Name: n, Kind: managed.KindSkill})
			}
			for _, n := range p.Rules {
				out = append(out, consumermanifest.ProfileMember{Name: n, Kind: managed.KindRule})
			}
			for _, n := range p.Agents {
				out = append(out, consumermanifest.ProfileMember{Name: n, Kind: managed.KindAgent})
			}
			for _, n := range p.Hooks {
				out = append(out, consumermanifest.ProfileMember{Name: n, Kind: managed.KindHook})
			}
			return out, nil
		}
	}
	resolved, err := consumermanifest.Expand(manifest, reg, profileLookup)
	if err != nil {
		return Errorf(ExitInvalidUsage, "%v", err)
	}

	// Compute scope.
	scope, err := resolveScope("auto", rc)
	if err != nil {
		return err
	}
	_ = scope // currently always project for manifest-flow

	// Frozen detection.
	frozen, err := shouldFreeze(cmd)
	if err != nil {
		return err
	}

	// Frozen branch: read lock, diff, fail if divergence; else
	// materialize from lock.
	if frozen {
		lock, err := consumerlock.Read(rc.ProjectRoot)
		if err != nil {
			if os.IsNotExist(err) {
				return Errorf(ExitInvalidUsage, "frozen install requested but %s does not exist; run `fdh install` (without --frozen) to generate it", consumerlock.Filename)
			}
			return Wrap(ExitInvalidUsage, err)
		}
		divs := consumerlock.Diff(resolved, lock, nil)
		if len(divs) > 0 {
			msg := "manifest↔lock divergence:\n"
			for _, d := range divs {
				msg += "  - " + d.String() + "\n"
			}
			msg += "Run `fdh install` (without --frozen) to regenerate the lock."
			return Errorf(ExitInvalidUsage, "%s", msg)
		}
	}

	// Materialize each component.
	components, err := materializeResolved(rc.Ctx, reg, rc, resolved, info)
	if err != nil {
		return err
	}

	// Write lock with the new resolution.
	lock := consumerlock.Build(resolved, reg.HubCommit, time.Now(), manifest.Profile)
	lockWritten := false
	if !frozen {
		if err := consumerlock.Write(rc.ProjectRoot, lock); err != nil {
			if errors.Is(err, os.ErrPermission) {
				return Wrap(ExitPermission, err)
			}
			return Wrap(ExitGenericFailure, err)
		}
		lockWritten = true
	}

	// Update .gitignore.
	managedPaths := collectManagedPathsForGitignore(rc.ProjectRoot, nil)
	gitignoreUpdated := false
	if err := gitignore.Apply(rc.ProjectRoot, managedPaths); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return Wrap(ExitPermission, err)
		}
		return Wrap(ExitGenericFailure, err)
	}
	gitignoreUpdated = true

	// Update the per-machine state ledger with the project entry.
	if rc.HomeDir != "" {
		if s, sErr := state.Load(rc.HomeDir); sErr == nil {
			lockBody, _ := os.ReadFile(consumerlock.Filename) // best-effort relative path; ignored if missing
			_ = lockBody
			s.UpsertProject(rc.ProjectRoot, state.ProjectEntry{
				LockHash:     "",
				ManagedPaths: managedPaths,
			})
			s.HubCache = state.HubCache{
				LastPulled: time.Now().UTC(),
				Commit:     reg.HubCommit,
				URL:        hubURL,
			}
			_ = state.Save(rc.HomeDir, s)
		}
	}

	result := ManifestInstallResult{
		FromManifest:     true,
		Frozen:           frozen,
		HubCommit:        reg.HubCommit,
		ResolvedFrom:     manifest.Profile,
		Components:       components,
		LockWritten:      lockWritten,
		GitignoreUpdated: gitignoreUpdated,
	}
	if outputMode(cmd) == "json" {
		return emitJSON(cmd.OutOrStdout(), result)
	}
	return printManifestInstallTable(cmd.OutOrStdout(), result)
}

// loadOrGenerateManifest implements the three branches:
//   - manifest present → load + return
//   - manifest absent + markers present → auto-generate, write, print, return (nil, true)
//   - manifest absent + no markers → exit ≠ 0 suggesting `fdh init`
func loadOrGenerateManifest(cmd *cobra.Command, rc *runContext) (*consumermanifest.Manifest, bool, error) {
	m, err := consumermanifest.Load(rc.ProjectRoot)
	if err == nil {
		return m, false, nil
	}
	if !os.IsNotExist(err) {
		return nil, false, Wrap(ExitInvalidUsage, err)
	}

	// Manifest missing. Try legacy auto-gen.
	gen, err := consumermanifest.GenerateFromLegacy(rc.ProjectRoot)
	if err != nil {
		return nil, false, Wrap(ExitGenericFailure, err)
	}
	if !consumermanifest.HasAnyEntries(gen) {
		return nil, false, Errorf(ExitInvalidUsage,
			"no .fdh/manifest.yaml and no legacy markers detected; run `fdh init` to bootstrap")
	}
	if err := consumermanifest.Write(rc.ProjectRoot, gen); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, false, Wrap(ExitPermission, err)
		}
		return nil, false, Wrap(ExitGenericFailure, err)
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "Generated manifest from legacy state — please review and commit .fdh/manifest.yaml before re-running install")
	return nil, true, nil
}

// shouldFreeze applies the precedence rule:
// flag > FDH_FROZEN env > CI heuristic > default.
func shouldFreeze(cmd *cobra.Command) (bool, error) {
	flagFrozen, _ := cmd.Flags().GetBool("frozen")
	flagNoFrozen, _ := cmd.Flags().GetBool("no-frozen")
	if flagFrozen && flagNoFrozen {
		return false, Errorf(ExitInvalidUsage, "--frozen and --no-frozen are mutually exclusive")
	}
	if flagFrozen {
		return true, nil
	}
	if flagNoFrozen {
		return false, nil
	}
	if env := os.Getenv("FDH_FROZEN"); env != "" {
		return parseTruthy(env), nil
	}
	if isCI() {
		return true, nil
	}
	return false, nil
}

func parseTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func isCI() bool {
	for _, v := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "CIRCLECI", "JENKINS_URL", "TF_BUILD"} {
		val := os.Getenv(v)
		if val == "" {
			continue
		}
		if strings.EqualFold(val, "false") || val == "0" {
			continue
		}
		return true
	}
	return false
}

// materializeResolved runs each resolved component through the
// adapter pipeline (currently skill-only — rule/agent/hook adapters
// land in their own changes) and returns the per-component summary.
func materializeResolved(ctx context.Context, reg *hubregistry.Registry, rc *runContext, resolved []consumermanifest.ResolvedComponent, info BuildInfo) ([]ManifestComponent, error) {
	out := make([]ManifestComponent, 0, len(resolved))
	detected := detectedAgentIDs(rc)
	for _, r := range resolved {
		srcDir, err := reg.FetchComponent(ctx, r.Name, r.Kind)
		if err != nil {
			return nil, Wrap(ExitRegistryUnreach, fmt.Errorf("fetch %s/%s: %w", r.Kind, r.Name, err))
		}
		var materialized []string
		for _, agentID := range detected {
			if r.HubEntry != nil && !contains(r.HubEntry.AgentsSupported, agentID) {
				continue
			}
			res, err := installResolvedComponent(srcDir, r, agentID, rc, reg.HubCommit, info)
			if err != nil {
				return nil, Wrap(ExitGenericFailure, fmt.Errorf("install %s/%s for %s: %w", r.Kind, r.Name, agentID, err))
			}
			if res == nil {
				continue // kind not yet supported by adapter
			}
			materialized = append(materialized, res.TargetPath)
		}
		out = append(out, ManifestComponent{
			Name:         r.Name,
			Kind:         r.Kind,
			Version:      r.HubEntry.Version,
			HubPath:      r.HubEntry.Path,
			Materialized: materialized,
			Skipped:      len(materialized) == 0,
		})
	}
	return out, nil
}

// installResolvedComponent routes a single resolved component to
// the right adapter family by kind. Returns nil result (without
// error) when the kind has no adapter implementation yet.
func installResolvedComponent(srcDir string, r consumermanifest.ResolvedComponent, agentID string, rc *runContext, hubCommit string, info BuildInfo) (*adapters.InstallResult, error) {
	opts := adapters.InstallOpts{
		SkillName:      r.Name,
		ProjectRoot:    rc.ProjectRoot,
		HomeDir:        rc.HomeDir,
		Scope:          adapters.ScopeProject,
		HubVersion:     r.HubEntry.Version,
		HubCommit:      hubCommit,
		InstalledByFDH: info.Version,
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
	return nil, nil
}

func printManifestInstallTable(w io.Writer, r ManifestInstallResult) error {
	mode := "regenerated"
	if r.Frozen {
		mode = "frozen (read-only)"
	}
	fmt.Fprintf(w, "Manifest-flow install (%s) — hub @ %s\n", mode, short(r.HubCommit))
	if r.ResolvedFrom != "" {
		fmt.Fprintf(w, "  resolved from profile: %s\n", r.ResolvedFrom)
	}
	for _, c := range r.Components {
		if c.Skipped {
			fmt.Fprintf(w, "  %s/%s — skipped (kind not yet supported)\n", c.Kind, c.Name)
			continue
		}
		fmt.Fprintf(w, "  %s/%s @ %s — %d agent target(s)\n", c.Kind, c.Name, c.Version, len(c.Materialized))
	}
	if r.LockWritten {
		fmt.Fprintln(w, "Wrote .fdh/lock.yaml")
	}
	if r.GitignoreUpdated {
		fmt.Fprintln(w, "Updated .gitignore managed section")
	}
	return nil
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

// contains reports whether s is in slice xs.
func containsLocal(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// shadow contains() to avoid clashing with another helper in the package.
var _ = containsLocal
