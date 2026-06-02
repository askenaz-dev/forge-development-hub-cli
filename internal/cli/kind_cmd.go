package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Noun-first command surface (capability fdh-command-surface): `fdh <kind> <verb>`
// for the four primitives. Singular nouns are canonical; the plural is an alias.
// Global verbs (init/install/update/doctor/search) remain top-level and unchanged;
// `<kind> add` (install one component) is a distinct operation that reuses the
// install path. The authoring verbs (new/sync/share) are implemented skills-first.

var kindNouns = []struct{ singular, plural string }{
	{"skill", "skills"},
	{"rule", "rules"},
	{"agent", "agents"},
	{"hook", "hooks"},
}

func newKindCmds(info BuildInfo) []*cobra.Command {
	cmds := make([]*cobra.Command, 0, len(kindNouns))
	for _, kn := range kindNouns {
		cmds = append(cmds, newKindCmd(info, kn.singular, kn.plural))
	}
	return cmds
}

func newKindCmd(info BuildInfo, kind, plural string) *cobra.Command {
	parent := &cobra.Command{
		Use:     kind + " <verb>",
		Aliases: []string{plural},
		Short:   fmt.Sprintf("Author, share, and manage %s components", kind),
		Long: fmt.Sprintf(`Noun-first command group for the %q primitive.

Verbs:
  new <name>      Scaffold a new %s locally and materialize it into the selected agents
  sync <name>     Regenerate the materialized copies from the canonical source
  share <name>    Open a contribution pull request adding the %s to the hub (never auto-merged)
  add <name>      Install one %s from the hub into this project
  list            List installed %s components
  search <query>  Search the hub catalog
  remove <name>   Remove an installed %s (not yet implemented)
  show <name>     Show details of a %s (not yet implemented)

The plural form (%q) is accepted as an alias.`, kind, kind, kind, kind, kind, kind, kind, plural),
	}
	// An unknown verb under a known kind must fail (exit non-zero) with the
	// valid verb set, rather than silently printing help.
	parent.Args = cobra.ArbitraryArgs
	parent.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return Errorf(ExitInvalidUsage,
			"unknown verb %q for `fdh %s` (valid: new, share, sync, add, remove, list, show, search)", args[0], kind)
	}

	// --- new ---
	newCmd := &cobra.Command{
		Use:   "new <name>",
		Short: fmt.Sprintf("Scaffold a new %s locally and materialize it into the selected agents", kind),
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runKindNew(cmd, args, info, kind) },
	}
	newCmd.Flags().String("scope", "auto", "materialize scope: user|project|auto")
	newCmd.Flags().String("dir", "", "canonical source directory (default: <project>/.fdh/authoring/<name>)")
	newCmd.Flags().String("description", "", "component description (skips the prompt)")
	newCmd.Flags().StringSlice("agent", nil, "target agent id (repeatable); default: every detected agent valid for the kind")
	parent.AddCommand(newCmd)

	// --- sync ---
	syncCmd := &cobra.Command{
		Use:   "sync <name>",
		Short: fmt.Sprintf("Regenerate the materialized copies of a %s from its canonical source", kind),
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runKindSync(cmd, args, info, kind) },
	}
	syncCmd.Flags().String("scope", "auto", "materialize scope: user|project|auto")
	syncCmd.Flags().String("dir", "", "canonical source directory (default: <project>/.fdh/authoring/<name>)")
	syncCmd.Flags().StringSlice("agent", nil, "target agent id (repeatable); default: every detected agent valid for the kind")
	syncCmd.Flags().Bool("check", false, "report drift without overwriting")
	syncCmd.Flags().Bool("force", false, "overwrite materialized copies even if edited directly")
	parent.AddCommand(syncCmd)

	// --- share ---
	shareCmd := &cobra.Command{
		Use:   "share <name>",
		Short: fmt.Sprintf("Open a contribution PR adding a %s to the hub (never auto-merged)", kind),
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runKindShare(cmd, args, info, kind) },
	}
	shareCmd.Flags().String("dir", "", "canonical source directory (default: <project>/.fdh/authoring/<name>)")
	shareCmd.Flags().String("repo", "", "path to a local hub checkout to contribute through")
	shareCmd.Flags().String("owner-team", "", "owner_team for the registry entry (default: unassigned)")
	shareCmd.Flags().StringSlice("agent", nil, "agents_supported for the registry entry (default: all four for skill)")
	shareCmd.Flags().String("base", "main", "base branch to branch off in the hub repo")
	shareCmd.Flags().Bool("dry-run", false, "prepare the branch + commit locally but do not push or open a PR")
	parent.AddCommand(shareCmd)

	// --- add (install one) — reuses the install path ---
	addCmd := &cobra.Command{
		Use:   "add <name>[@version]",
		Short: fmt.Sprintf("Install one %s from the hub into this project", kind),
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runInstall(cmd, args, info) },
	}
	addCmd.Flags().StringSlice("agent", nil, "agent id to target (may be repeated)")
	addCmd.Flags().String("scope", "auto", "install scope: user|project|auto (default: project, rooted at the current directory)")
	addCmd.Flags().Bool("global", false, "install at user/home scope (~/.claude, …) instead of into the current project")
	parent.AddCommand(addCmd)

	// --- list — reuses the list path ---
	listCmd := &cobra.Command{
		Use:   "list",
		Short: fmt.Sprintf("List installed %s components", kind),
		RunE:  func(cmd *cobra.Command, args []string) error { return runList(cmd, args, info) },
	}
	listCmd.Flags().StringSlice("agent", nil, "limit to specific agents (may be repeated)")
	listCmd.Flags().String("scope", "all", "scope to list: user|project|all")
	parent.AddCommand(listCmd)

	// --- search — reuses the search path ---
	searchCmd := &cobra.Command{
		Use:   "search <query>",
		Short: fmt.Sprintf("Search the hub catalog for %s components", kind),
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runSearch(cmd, args, info) },
	}
	parent.AddCommand(searchCmd)

	// --- remove / show — registered for grammar completeness, not yet wired ---
	for _, nv := range []struct{ use, label string }{
		{"remove <name>", "Remove"},
		{"show <name>", "Show"},
	} {
		verb := strings.Fields(nv.use)[0]
		parent.AddCommand(&cobra.Command{
			Use:   nv.use,
			Short: fmt.Sprintf("%s a %s (not yet implemented)", nv.label, kind),
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return Errorf(ExitInvalidUsage, "`fdh %s %s` is not implemented yet", kind, verb)
			},
		})
	}

	return parent
}
