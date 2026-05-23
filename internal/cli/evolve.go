package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/falabella/fdh/pkg/instincts"
	"github.com/spf13/cobra"
)

// newEvolveCmd wires `fdh evolve` — the admin-facing clustering command.
func newEvolveCmd() *cobra.Command {
	var from string
	var includeLocal bool
	var outputDir string
	var minClusterSize int
	var minAvgConfidence float64
	var similarityThreshold float64

	cmd := &cobra.Command{
		Use:   "evolve",
		Short: "Cluster local instincts (and/or an imported bundle) into skill drafts",
		Long: `Group local instincts by similarity and generate SKILL.md drafts under
./fdh-evolve-output/ for admin review. The clustering is deterministic,
rule-based (Jaccard over tags + title keywords). No LLMs, no network.

A draft is generated for each cluster meeting --min-cluster-size and
--min-avg-confidence. Each draft starts with a "⚠️ DRAFT" banner; CI of
the hub blocks PRs that try to merge a SKILL.md still containing it,
so admins must curate before opening the PR.

Inputs:
  default            cluster ~/.fdh/instincts/* only
  --from <file>      cluster contents of an imported bundle only
  --from <file>     +  --include-local   cluster the union of both`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var items []*instincts.Instinct

			if from != "" {
				format := instincts.DetectBundleFormat(from)
				if format == instincts.BundleUnknown {
					return fmt.Errorf("unknown bundle format for %s", from)
				}
				fromItems, err := readBundle(from, format)
				if err != nil {
					return fmt.Errorf("read --from bundle: %w", err)
				}
				items = append(items, fromItems...)
				if includeLocal {
					locals, err := instincts.ReadAll()
					if err != nil {
						return err
					}
					items = append(items, locals...)
				}
			} else {
				locals, err := instincts.ReadAll()
				if err != nil {
					return err
				}
				items = locals
			}

			if len(items) == 0 {
				return fmt.Errorf("no instincts to cluster")
			}

			opts := instincts.ClusterOptions{
				MinClusterSize:      minClusterSize,
				MinAvgConfidence:    minAvgConfidence,
				SimilarityThreshold: similarityThreshold,
			}
			candidates, skipped := instincts.ClusterAll(items, opts)

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "fdh evolve: input=%d, candidates=%d, skipped=%d\n",
				len(items), len(candidates), len(skipped))
			for _, sk := range skipped {
				fmt.Fprintf(out, "  - skipped cluster (domain=%s, %d members): %s\n",
					sk.Domain, len(sk.Members), sk.SkippedReason)
			}

			if len(candidates) == 0 {
				fmt.Fprintln(out, "no clusters met the thresholds; nothing written.")
				return nil
			}

			if outputDir == "" {
				outputDir = filepath.Join(".", "fdh-evolve-output")
			}
			now := time.Now().UTC()
			command := "fdh evolve " + strings.Join(args, " ")
			if from != "" {
				command += " --from " + from
				if includeLocal {
					command += " --include-local"
				}
			}

			// Resolve slug collisions deterministically (append -2, -3, …).
			usedSlugs := map[string]int{}
			for _, c := range candidates {
				slug := c.Slug()
				if n, ok := usedSlugs[slug]; ok {
					n++
					usedSlugs[slug] = n
					altSlug := fmt.Sprintf("%s-%d", slug, n)
					fmt.Fprintf(cmd.ErrOrStderr(), "slug collision: '%s' already used; writing as '%s'\n", slug, altSlug)
					slug = altSlug
				} else {
					usedSlugs[slug] = 1
				}
				dir := filepath.Join(outputDir, slug)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return err
				}
				path := filepath.Join(dir, "SKILL.md")
				content := c.RenderDraft(instincts.DraftOptions{
					GeneratedAt:   now,
					EvolveCommand: command,
				})
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					return err
				}
				fmt.Fprintf(out, "  + %s (domain=%s, %d sources, avg conf=%.2f)\n",
					path, c.Domain, len(c.Members), c.AvgConfidence)
			}

			_ = instincts.MutateState(func(s *instincts.StateInstincts) {
				s.LastEvolve = &now
				s.EvolveRuns++
			})

			// Stable ordering of summary lines.
			sort.Strings(nil)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "Read instincts from a bundle file instead of (or with) ~/.fdh/instincts/")
	cmd.Flags().BoolVar(&includeLocal, "include-local", false, "Combine --from contents with local instincts")
	cmd.Flags().StringVar(&outputDir, "output-dir", "", "Where to write drafts (default ./fdh-evolve-output)")
	cmd.Flags().IntVar(&minClusterSize, "min-cluster-size", 3, "Minimum cluster size to generate a draft")
	cmd.Flags().Float64Var(&minAvgConfidence, "min-avg-confidence", 0.6, "Minimum avg confidence to generate a draft")
	cmd.Flags().Float64Var(&similarityThreshold, "similarity-threshold", 0.5, "Pairwise similarity for cluster membership")
	return cmd
}
