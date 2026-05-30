package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/forge/fdh/pkg/adapters"
	"github.com/forge/fdh/pkg/gitignore"
	"github.com/forge/fdh/pkg/managed"
)

// UninstallResult is the JSON shape emitted by `uninstall --json`.
type UninstallResult struct {
	Name             string   `json:"name"`
	Scope            string   `json:"scope"`
	DryRun           bool     `json:"dry_run,omitempty"`
	Removed          []string `json:"removed"`
	GitignoreUpdated bool     `json:"gitignore_updated,omitempty"`
}

func newUninstallCmd(info BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall <name>",
		Short: "Remove an installed component and refresh the managed .gitignore",
		Long: `Walk the install directories for every known adapter, find the
component matching <name> (read from .fdh-managed.yaml or the legacy
.skill-version marker), delete its files, and rewrite the managed
.gitignore section to drop the removed path. Use --dry-run to preview.

Scope defaults to "auto": picks project when a project root is
detectable, falls back to user. --scope user|project overrides.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUninstall(cmd, args, info)
		},
	}
	cmd.Flags().String("scope", "auto", "uninstall scope: user|project|auto")
	cmd.Flags().Bool("dry-run", false, "describe what would be removed without touching the filesystem")
	return cmd
}

func runUninstall(cmd *cobra.Command, args []string, info BuildInfo) error {
	_ = info
	verbose, _ := cmd.PersistentFlags().GetBool("verbose")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	scopeStr, _ := cmd.Flags().GetString("scope")

	rc, err := buildRunContext(cmd.Context(), info, verbose)
	if err != nil {
		return err
	}
	scope, err := resolveScope(scopeStr, rc)
	if err != nil {
		return err
	}

	name := args[0]

	// Discover candidates: walk known install dirs for the scope and
	// find markers whose Name matches.
	candidates, err := findUninstallCandidates(rc, scope, name)
	if err != nil {
		return Wrap(ExitGenericFailure, err)
	}

	result := UninstallResult{
		Name:   name,
		Scope:  string(scope),
		DryRun: dryRun,
	}

	if len(candidates) == 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "no managed components named %q found in scope %s\n", name, scope)
		if outputMode(cmd) == "json" {
			return emitJSON(cmd.OutOrStdout(), result)
		}
		return nil
	}

	// Remove each candidate's content.
	for _, c := range candidates {
		if !dryRun {
			if err := os.RemoveAll(c.removePath); err != nil {
				return Wrap(ExitGenericFailure, fmt.Errorf("remove %s: %w", c.removePath, err))
			}
			// For flat: also remove the marker sibling explicitly in
			// case removePath is a single file with the sibling marker
			// still present.
			if c.markerPath != "" && c.markerPath != c.removePath {
				_ = os.Remove(c.markerPath)
			}
		}
		result.Removed = append(result.Removed, c.removePath)
	}

	// Refresh the .gitignore managed block at project scope.
	if !dryRun && scope == adapters.ScopeProject && rc.ProjectRoot != "" {
		managedPaths := collectManagedPathsForGitignore(rc.ProjectRoot, nil)
		if err := gitignore.Apply(rc.ProjectRoot, managedPaths); err != nil {
			return Wrap(ExitGenericFailure, fmt.Errorf("update .gitignore: %w", err))
		}
		result.GitignoreUpdated = true
	}

	if outputMode(cmd) == "json" {
		return emitJSON(cmd.OutOrStdout(), result)
	}
	return printUninstallTable(cmd.OutOrStdout(), result)
}

// uninstallCandidate is one matched component+adapter location.
type uninstallCandidate struct {
	agent      string
	kind       string
	removePath string // file or directory to delete
	markerPath string // marker file path (for flat siblings)
}

func findUninstallCandidates(rc *runContext, scope adapters.Scope, name string) ([]uninstallCandidate, error) {
	var out []uninstallCandidate
	for _, a := range adapters.AllSkillAdapters() {
		root, err := adapterScopeRoot(a, rc.HomeDir, rc.ProjectRoot, scope)
		if err != nil {
			continue
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		if a.SupportsSubresources() {
			cands, err := matchDirectory(a.Agent(), root, name)
			if err != nil {
				return nil, err
			}
			out = append(out, cands...)
		} else {
			cands, err := matchFlat(a.Agent(), root, name)
			if err != nil {
				return nil, err
			}
			out = append(out, cands...)
		}
	}
	return out, nil
}

func matchDirectory(agent, root, name string) ([]uninstallCandidate, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []uninstallCandidate
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		markerPath := filepath.Join(dir, managed.Filename)
		legacy := filepath.Join(dir, ".skill-version")
		var m managed.Marker
		var mPath string
		if mr, err := managed.Read(markerPath); err == nil {
			m = mr
			mPath = markerPath
		} else if mr, err := managed.Read(legacy); err == nil {
			m = mr
			mPath = legacy
		} else {
			continue
		}
		if m.Name != name {
			continue
		}
		if m.Agent != "" && m.Agent != agent {
			continue
		}
		out = append(out, uninstallCandidate{
			agent:      agent,
			kind:       valueOr(m.Kind, managed.KindSkill),
			removePath: dir,
			markerPath: mPath,
		})
	}
	return out, nil
}

func matchFlat(agent, root, name string) ([]uninstallCandidate, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []uninstallCandidate
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fname := e.Name()
		isCanonical := managed.IsManagedFilename(fname) && fname != managed.Filename
		isLegacy := strings.HasPrefix(fname, ".skill-version-") && fname != ".skill-version-"
		if !isCanonical && !isLegacy {
			continue
		}
		markerPath := filepath.Join(root, fname)
		m, err := managed.Read(markerPath)
		if err != nil {
			continue
		}
		if m.Name != name {
			continue
		}
		if m.Agent != "" && m.Agent != agent {
			continue
		}
		// Recover the materialized file: strip the marker suffix.
		var target string
		if isCanonical {
			target = filepath.Join(root, strings.TrimSuffix(fname, ".fdh-managed.yaml"))
		} else {
			// Legacy `.skill-version-<name>` => `<name>.prompt.md` for
			// copilot, `<name>.md` for opencode. Recover via convention.
			plain := strings.TrimPrefix(fname, ".skill-version-")
			switch agent {
			case "copilot":
				target = filepath.Join(root, plain+".prompt.md")
			case "opencode":
				target = filepath.Join(root, plain+".md")
			default:
				target = filepath.Join(root, plain)
			}
		}
		out = append(out, uninstallCandidate{
			agent:      agent,
			kind:       valueOr(m.Kind, managed.KindSkill),
			removePath: target,
			markerPath: markerPath,
		})
	}
	return out, nil
}

func valueOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// AllSkillAdapters returns every shipped adapter. Defined here as a
// convenience because pkg/adapters does not export the whole list as
// a single helper.
//
// (Function lives in update_scan.go's neighborhood; redefine if a
// cyclic ref appears.)
func printUninstallTable(w io.Writer, r UninstallResult) error {
	if r.DryRun {
		fmt.Fprintf(w, "Would remove %d path(s) for %s:\n", len(r.Removed), r.Name)
	} else {
		fmt.Fprintf(w, "Removed %d path(s) for %s:\n", len(r.Removed), r.Name)
	}
	for _, p := range sortedCopy(r.Removed) {
		fmt.Fprintf(w, "  - %s\n", p)
	}
	if r.GitignoreUpdated {
		fmt.Fprintln(w, "Updated .gitignore managed section.")
	}
	return nil
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
