package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/forge/fdh/pkg/state"
)

// ListInstalledResult is the JSON shape emitted by
// `fdh list-installed --json`.
type ListInstalledResult struct {
	UserScopeInstalls state.KindBuckets             `json:"user_scope_installs"`
	Projects          map[string]state.ProjectEntry `json:"projects,omitempty"`
}

func newListInstalledCmd(info BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list-installed",
		Short: "Show components installed at user scope (and optionally per project)",
		Long: `Read ~/.fdh/state.json and print a summary of installed components.

By default shows only user-scope installs. Use --projects to include
project entries; --all expands both. --kind filters by skill, rule,
agent, or hook.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runListInstalled(cmd, args, info)
		},
	}
	cmd.Flags().Bool("projects", false, "include projects registered in state")
	cmd.Flags().Bool("all", false, "include both user-scope and projects")
	cmd.Flags().String("kind", "", "filter by kind: skill|rule|agent|hook")
	return cmd
}

func runListInstalled(cmd *cobra.Command, args []string, info BuildInfo) error {
	_ = args
	verbose, _ := cmd.PersistentFlags().GetBool("verbose")
	rc, err := buildRunContext(cmd.Context(), info, verbose)
	if err != nil {
		return err
	}
	s, err := state.Load(rc.HomeDir)
	if err != nil {
		return Wrap(ExitGenericFailure, err)
	}

	includeProjects, _ := cmd.Flags().GetBool("projects")
	all, _ := cmd.Flags().GetBool("all")
	kind, _ := cmd.Flags().GetString("kind")
	if all {
		includeProjects = true
	}

	buckets := filterByKind(s.UserScopeInstalls, kind)
	result := ListInstalledResult{UserScopeInstalls: buckets}
	if includeProjects {
		result.Projects = s.Projects
	}

	if outputMode(cmd) == "json" {
		return emitJSON(cmd.OutOrStdout(), result)
	}
	return printListInstalledTable(cmd.OutOrStdout(), result, includeProjects)
}

func filterByKind(in state.KindBuckets, kind string) state.KindBuckets {
	if kind == "" {
		return in
	}
	out := state.KindBuckets{}
	switch kind {
	case "skill":
		out.Skills = in.Skills
	case "rule":
		out.Rules = in.Rules
	case "agent":
		out.Agents = in.Agents
	case "hook":
		out.Hooks = in.Hooks
	}
	return out
}

func printListInstalledTable(w io.Writer, r ListInstalledResult, withProjects bool) error {
	fmt.Fprintln(w, "User-scope installs:")
	printBucket(w, "skills", r.UserScopeInstalls.Skills)
	printBucket(w, "rules", r.UserScopeInstalls.Rules)
	printBucket(w, "agents", r.UserScopeInstalls.Agents)
	printBucket(w, "hooks", r.UserScopeInstalls.Hooks)
	if withProjects {
		fmt.Fprintln(w, "\nProjects:")
		if len(r.Projects) == 0 {
			fmt.Fprintln(w, "  (none registered)")
		}
		for path, p := range r.Projects {
			fmt.Fprintf(w, "  - %s (lock_hash=%s, %d managed paths, last_install=%s)\n",
				path, short(p.LockHash), len(p.ManagedPaths), p.LastInstallAt.Format("2006-01-02"))
		}
	}
	return nil
}

func printBucket(w io.Writer, label string, entries []state.InstallEntry) {
	fmt.Fprintf(w, "  %s:\n", label)
	if len(entries) == 0 {
		fmt.Fprintln(w, "    (none)")
		return
	}
	for _, e := range entries {
		fmt.Fprintf(w, "    - %s@%s  (%s)\n", e.Name, e.Version, e.Path)
	}
}
