package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/forge/fdh/pkg/registry"
)

// SearchHit mirrors registry.SkillSummary with explicit JSON tags.
type SearchHit struct {
	Namespace     string   `json:"namespace"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	OwnerTeam     string   `json:"owner_team"`
	Tags          []string `json:"tags,omitempty"`
	LatestVersion string   `json:"latest_version"`
	LatestHash    string   `json:"latest_hash"`
	ScanStatus    string   `json:"scan_status"`
}

func newSearchCmd(info BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search the hub catalog",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSearch(cmd, args, info)
		},
	}
	return cmd
}

func runSearch(cmd *cobra.Command, args []string, info BuildInfo) error {
	verbose, _ := cmd.PersistentFlags().GetBool("verbose")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rc, err := buildRunContext(ctx, info, verbose)
	if err != nil {
		return err
	}
	if rc.Registry == nil {
		return Errorf(ExitInvalidUsage, "no registry configured")
	}

	results, err := rc.Registry.Search(rc.Ctx, args[0])
	if err != nil {
		var unreach registry.RegistryUnreachable
		if errors.As(err, &unreach) {
			return Wrap(ExitRegistryUnreach, err)
		}
		return Wrap(ExitGenericFailure, err)
	}

	hits := make([]SearchHit, 0, len(results))
	for _, r := range results {
		hits = append(hits, SearchHit{
			Namespace:     r.Namespace,
			Name:          r.Name,
			Description:   r.Description,
			OwnerTeam:     r.OwnerTeam,
			Tags:          r.Tags,
			LatestVersion: r.LatestVersion,
			LatestHash:    r.LatestHash,
			ScanStatus:    r.ScanStatus,
		})
	}

	if outputMode(cmd) == "json" {
		return emitJSON(cmd.OutOrStdout(), hits)
	}
	return printSearchTable(cmd.OutOrStdout(), hits)
}

func printSearchTable(w io.Writer, hits []SearchHit) error {
	if len(hits) == 0 {
		fmt.Fprintln(w, "No matches.")
		return nil
	}
	headers := []string{"NAMESPACE/NAME", "VERSION", "SCAN", "DESCRIPTION"}
	rows := make([][]string, 0, len(hits))
	for _, h := range hits {
		rows = append(rows, []string{
			h.Namespace + "/" + h.Name,
			h.LatestVersion,
			h.ScanStatus,
			truncate(h.Description, 60),
		})
	}
	return printTable(w, headers, rows)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
