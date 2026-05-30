package cli

import (
	"github.com/spf13/cobra"
)

// runUpdate is implemented in update_run.go (added in Section 6 of
// this change). The constructor below references it so wiring
// stays in one place.

// newUpdateCmd registers the `fdh update` subcommand. The actual
// implementation (planner, drift detection, executor) lives in the
// other files of this package — see update_plan.go and update_run.go.
func newUpdateCmd(info BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Refresh installed skills from the hub",
		Long: `Walk every directory a known agent reads from, find .skill-version markers,
compare the recorded hub commit + content hash against the hub's current HEAD,
and propose an update plan. Confirm interactively (or pass --yes), then apply.

Local edits inside an installed skill are detected via content_hash and
preserved — pass --force to overwrite them.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(cmd, args, info)
		},
	}
	cmd.Flags().Bool("yes", false, "skip confirmation prompt")
	cmd.Flags().Bool("dry-run", false, "compute the plan without writing")
	cmd.Flags().StringSlice("skill", nil, "limit updates to the named skill(s)")
	cmd.Flags().StringSlice("agent", nil, "limit updates to the named agent(s)")
	cmd.Flags().Bool("force", false, "overwrite locally-edited skills (drift)")
	cmd.Flags().Bool("include-new-defaults", false, "also propose installing default:true skills not yet present")
	cmd.Flags().String("kind", "", "limit updates to the named kind (skill|rule|agent|hook)")
	return cmd
}
