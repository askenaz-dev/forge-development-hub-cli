package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/forge/fdh/pkg/state"
)

// RepairResult is the JSON shape emitted by `fdh repair --json`.
type RepairResult struct {
	DryRun  bool             `json:"dry_run,omitempty"`
	Issues  []RepairIssue    `json:"issues"`
	Cleaned []string         `json:"cleaned,omitempty"`
}

// RepairIssue is one divergence detected during repair.
type RepairIssue struct {
	Kind        string `json:"kind"` // missing-path | orphan-project
	Path        string `json:"path"`
	Description string `json:"description"`
}

func newRepairCmd(info BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Reconcile ~/.fdh/state.json against the filesystem",
		Long: `Walk projects registered in state.json. Report cases where
managed paths no longer exist on disk, or projects whose root
directory itself has vanished. --orphans cleans the latter from state.
--dry-run reports without modifying state.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRepair(cmd, args, info)
		},
	}
	cmd.Flags().Bool("dry-run", false, "report drift without modifying state")
	cmd.Flags().Bool("orphans", false, "remove state entries whose project directory no longer exists")
	return cmd
}

func runRepair(cmd *cobra.Command, args []string, info BuildInfo) error {
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
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	orphans, _ := cmd.Flags().GetBool("orphans")

	result := RepairResult{DryRun: dryRun}
	var toRemove []string
	for projPath, entry := range s.Projects {
		if _, err := os.Stat(projPath); err != nil && os.IsNotExist(err) {
			result.Issues = append(result.Issues, RepairIssue{
				Kind:        "orphan-project",
				Path:        projPath,
				Description: "project directory no longer exists",
			})
			if orphans && !dryRun {
				toRemove = append(toRemove, projPath)
			}
			continue
		}
		for _, mp := range entry.ManagedPaths {
			abs := mp
			if !isAbsolute(mp) {
				abs = joinPath(projPath, mp)
			}
			if _, err := os.Stat(abs); err != nil && os.IsNotExist(err) {
				result.Issues = append(result.Issues, RepairIssue{
					Kind:        "missing-path",
					Path:        abs,
					Description: fmt.Sprintf("managed path missing in project %s", projPath),
				})
			}
		}
	}

	for _, p := range toRemove {
		s.RemoveProject(p)
		result.Cleaned = append(result.Cleaned, p)
	}
	if !dryRun && len(toRemove) > 0 {
		if err := state.Save(rc.HomeDir, s); err != nil {
			return Wrap(ExitGenericFailure, err)
		}
	}

	if outputMode(cmd) == "json" {
		return emitJSON(cmd.OutOrStdout(), result)
	}
	return printRepairTable(cmd.OutOrStdout(), result)
}

func printRepairTable(w io.Writer, r RepairResult) error {
	if len(r.Issues) == 0 {
		fmt.Fprintln(w, "No drift detected.")
		return nil
	}
	fmt.Fprintf(w, "Detected %d issue(s):\n", len(r.Issues))
	for _, i := range r.Issues {
		fmt.Fprintf(w, "  [%s] %s — %s\n", i.Kind, i.Path, i.Description)
	}
	if len(r.Cleaned) > 0 {
		fmt.Fprintf(w, "Cleaned %d orphan project(s) from state.\n", len(r.Cleaned))
	}
	return nil
}

func isAbsolute(p string) bool {
	if len(p) == 0 {
		return false
	}
	return p[0] == '/' || (len(p) > 1 && p[1] == ':')
}

func joinPath(a, b string) string {
	if a == "" {
		return b
	}
	if len(a) > 0 && (a[len(a)-1] == '/' || a[len(a)-1] == '\\') {
		return a + b
	}
	return a + string(os.PathSeparator) + b
}
