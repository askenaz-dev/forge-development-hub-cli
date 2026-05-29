package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/forge/fdh/pkg/bundle"
)

// newBundleHashCmd prints the canonical content hash of a bundle directory.
// It is the enabler for the release-time signing pipeline (capability
// bundle-signing): CI computes the hash with this command and signs it with
// cosign, guaranteeing the signed artifact equals the producer's content_hash.
func newBundleHashCmd(_ BuildInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "bundle-hash <dir>",
		Short: "Print the canonical SHA-256 content hash of a component bundle directory",
		Long: `Compute the canonical, OS-independent content hash of a bundle directory
using the same algorithm as the registry producer and the install-time verifier
(bundle.HashDir). This is the artifact the release pipeline signs with cosign
and the value recorded as content_hash in the manifest.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := bundle.HashDir(args[0])
			if err != nil {
				return Wrap(ExitGenericFailure, err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), h)
			return nil
		},
	}
}
