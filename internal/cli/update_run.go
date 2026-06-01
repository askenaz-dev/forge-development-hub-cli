package cli

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/forge/fdh/pkg/adapters"
	"github.com/forge/fdh/pkg/hubregistry"
)

// UpdateResult is the JSON shape `fdh update --json` emits. Same
// additive-only contract as InitResult.
type UpdateResult struct {
	HubCommit string             `json:"hub_commit"`
	Plan      []UpdatePlanAction `json:"plan"`
	Applied   []UpdateOutcome    `json:"applied,omitempty"`
	Skipped   []UpdateOutcome    `json:"skipped,omitempty"`
	Failed    []UpdateFailure    `json:"failed,omitempty"`
	DryRun    bool               `json:"dry_run,omitempty"`
}

// UpdateOutcome records one applied/skipped action with the marker
// values from after the operation. For "applied" this reflects the
// new on-disk state; for "skipped" it mirrors what was already there.
type UpdateOutcome struct {
	Skill       string `json:"skill"`
	Agent       string `json:"agent"`
	Action      string `json:"action"`
	ContentHash string `json:"content_hash"`
	Reason      string `json:"reason,omitempty"`
}

// UpdateFailure records one (skill, agent) the apply pass could not
// complete.
type UpdateFailure struct {
	Skill string `json:"skill"`
	Agent string `json:"agent"`
	Error string `json:"error"`
}

func runUpdate(cmd *cobra.Command, _ []string, info BuildInfo) error {
	skipDoctor := false
	_ = skipDoctor
	yes, _ := cmd.Flags().GetBool("yes")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	skillFilter, _ := cmd.Flags().GetStringSlice("skill")
	agentFilter, _ := cmd.Flags().GetStringSlice("agent")
	force, _ := cmd.Flags().GetBool("force")
	kindFilter, _ := cmd.Flags().GetString("kind")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rc, err := buildRunContext(ctx, info, false)
	if err != nil {
		return err
	}
	hubURL := hubURLFromConfigOrFlag(rc)
	if hubURL == "" {
		return Errorf(ExitInvalidUsage, "no registry URL configured; run `fdh init --registry-url <url>` first")
	}

	reg, err := loadHubWithRecovery(ctx, cmd.ErrOrStderr(), hubURL, rc.Verbose)
	if err != nil {
		return Wrap(ExitRegistryUnreach, err)
	}

	installed, err := findInstalledSkills(rc.HomeDir, rc.ProjectRoot)
	if err != nil {
		return Wrap(ExitGenericFailure, err)
	}
	if len(installed) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No installed skills found. Run `fdh init` first.")
		return nil
	}

	// Apply kind filter on installed markers (skip non-skill kinds
	// when caller passed --kind=skill, etc.).
	if kindFilter != "" {
		filtered := installed[:0]
		for _, ins := range installed {
			mKind := ins.Marker.Kind
			if mKind == "" {
				mKind = "skill"
			}
			if mKind == kindFilter {
				filtered = append(filtered, ins)
			}
		}
		installed = filtered
		if len(installed) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "No installed components of kind %q.\n", kindFilter)
			return nil
		}
	}

	plan, err := planUpdates(ctx, installed,
		reg,
		flagSetToMap(skillFilter),
		flagSetToMap(agentFilter),
		force,
	)
	if err != nil {
		return Wrap(ExitGenericFailure, err)
	}

	result := UpdateResult{HubCommit: reg.HubCommit, Plan: plan, DryRun: dryRun}

	if outputMode(cmd) != "json" {
		printUpdatePlan(cmd.OutOrStdout(), plan, dryRun)
	}

	// If there are no refreshes, we still emit the plan for visibility
	// but skip the confirmation+apply pass.
	anyRefresh := false
	for _, a := range plan {
		if a.Action == "refresh" {
			anyRefresh = true
			break
		}
	}
	if !anyRefresh {
		if outputMode(cmd) == "json" {
			return emitJSON(cmd.OutOrStdout(), result)
		}
		return nil
	}
	if !yes && !dryRun && outputMode(cmd) != "json" {
		fmt.Fprint(cmd.OutOrStdout(), "\nApply these updates? (y/N) ")
		if !stdinIsTTY() {
			fmt.Fprintln(cmd.OutOrStdout(), "\nstdin not a TTY; rerun with --yes or --dry-run to act.")
			return nil
		}
		var ans string
		if _, err := fmt.Fscanln(cmd.InOrStdin(), &ans); err != nil || !strings.EqualFold(strings.TrimSpace(ans), "y") {
			fmt.Fprintln(cmd.OutOrStdout(), "Canceled.")
			return nil
		}
	}

	// Apply.
	for _, action := range plan {
		switch action.Action {
		case "up-to-date":
			result.Skipped = append(result.Skipped, UpdateOutcome{
				Skill: action.Skill, Agent: action.Agent,
				Action: action.Action,
			})
		case "drift":
			result.Skipped = append(result.Skipped, UpdateOutcome{
				Skill: action.Skill, Agent: action.Agent,
				Action: action.Action,
				Reason: action.Reason,
			})
		case "vanished":
			result.Skipped = append(result.Skipped, UpdateOutcome{
				Skill: action.Skill, Agent: action.Agent,
				Action: action.Action,
				Reason: action.Reason,
			})
		case "refresh":
			res, err := applyUpdate(ctx, reg, action.Skill, action.Agent, rc, info.Version, dryRun)
			if err != nil {
				result.Failed = append(result.Failed, UpdateFailure{
					Skill: action.Skill, Agent: action.Agent,
					Error: err.Error(),
				})
				continue
			}
			result.Applied = append(result.Applied, UpdateOutcome{
				Skill: action.Skill, Agent: action.Agent,
				Action: action.Action, ContentHash: res.ContentHash,
			})
		}
	}

	// Tier 1: one component.updated per distinct skill actually refreshed
	// (deduped across agents). Coordinate + outcome only.
	if !dryRun {
		seen := map[string]bool{}
		for _, a := range result.Applied {
			if seen[a.Skill] {
				continue
			}
			seen[a.Skill] = true
			emitTelemetry(cmd, EventNameUpdated, map[string]string{
				"kind": valueOr(kindFilter, "skill"), "name": a.Skill,
				"os": goos(), "cli_version": info.Version,
			})
		}
	}

	if outputMode(cmd) == "json" {
		if err := emitJSON(cmd.OutOrStdout(), result); err != nil {
			return Wrap(ExitGenericFailure, err)
		}
	} else {
		printUpdateSummary(cmd.OutOrStdout(), result)
	}
	return nil
}

// applyUpdate re-runs the adapter's Install for one (skill, agent),
// overwriting the existing on-disk content. The marker is rewritten
// as part of Install.
func applyUpdate(
	ctx context.Context,
	reg *hubregistry.Registry,
	skillName, agentID string,
	rc *runContext,
	fdhVersion string,
	dryRun bool,
) (adapters.InstallResult, error) {
	entry := reg.ComponentByName(skillName, hubregistry.KindSkill)
	if entry == nil {
		return adapters.InstallResult{}, fmt.Errorf("skill %s vanished from hub", skillName)
	}
	adapter := adapters.SkillAdapterByID(agentID)
	if adapter == nil {
		return adapters.InstallResult{}, fmt.Errorf("agent %s has no adapter", agentID)
	}
	srcDir, err := reg.FetchComponent(ctx, skillName, hubregistry.KindSkill)
	if err != nil {
		return adapters.InstallResult{}, err
	}
	scope, err := resolveScope("auto", rc)
	if err != nil {
		return adapters.InstallResult{}, err
	}
	opts := adapters.InstallOpts{
		SkillName:      skillName,
		ProjectRoot:    rc.ProjectRoot,
		HomeDir:        rc.HomeDir,
		Scope:          scope,
		HubVersion:     entry.Version,
		HubCommit:      reg.HubCommit,
		InstalledByFDH: fdhVersion,
		Overwrite:      true,
		DryRun:         dryRun,
	}
	return adapter.Install(srcDir, opts)
}

func loadHubWithRecovery(ctx context.Context, stderr io.Writer, url string, verbose bool) (*hubregistry.Registry, error) {
	opts := hubregistry.LoadOptions{}
	if verbose {
		opts.Logger = func(line string) { fmt.Fprintln(stderr, "[hub] "+line) }
	}
	reg, err := hubregistry.Load(ctx, url, opts)
	if err == nil {
		return reg, nil
	}
	var corrupt *hubregistry.CorruptCacheError
	if errors.As(err, &corrupt) {
		fmt.Fprintf(stderr, "warning: hub cache at %s was corrupt; re-cloning…\n", corrupt.CacheDir)
		if err := hubregistry.RecoverFromCorruption(corrupt.CacheDir); err != nil {
			return nil, err
		}
		return hubregistry.Load(ctx, url, opts)
	}
	return nil, err
}

func printUpdatePlan(w io.Writer, plan []UpdatePlanAction, dryRun bool) {
	if dryRun {
		fmt.Fprintln(w, "fdh update (dry-run)")
	} else {
		fmt.Fprintln(w, "fdh update")
	}
	for _, a := range plan {
		fmt.Fprintf(w, "  %-12s %s -> %s\n", a.Action, a.Skill, a.Agent)
		if a.Reason != "" {
			fmt.Fprintf(w, "      reason: %s\n", a.Reason)
		}
		if n := len(a.Files.Added) + len(a.Files.Modified) + len(a.Files.Deleted); n > 0 {
			fmt.Fprintf(w, "      files:  +%d ~%d -%d\n",
				len(a.Files.Added), len(a.Files.Modified), len(a.Files.Deleted))
		}
	}
}

func printUpdateSummary(w io.Writer, r UpdateResult) {
	fmt.Fprintf(w, "\nApplied: %d  Skipped: %d  Failed: %d\n",
		len(r.Applied), len(r.Skipped), len(r.Failed))
	for _, f := range r.Failed {
		fmt.Fprintf(w, "  FAIL  %s -> %s: %s\n", f.Skill, f.Agent, f.Error)
	}
}

// newSHA256 is the in-package factory used by update_plan.go.
func newSHA256() hash.Hash { return sha256.New() }
