package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/forge/fdh/pkg/scan"
)

func newScanCmd(info BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan [path...]",
		Short: "Audit components for secrets, hook injection, and other security smells",
		Long: `Walk the given paths (default: current directory) and run rule-based
detectors looking for GitHub tokens, AWS access keys, JWTs, URL-
embedded credentials, and command-injection patterns in hook scripts.

Exits 0 when no findings are at severity "error", non-zero otherwise.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScan(cmd, args, info)
		},
	}
	return cmd
}

func runScan(cmd *cobra.Command, args []string, _ BuildInfo) error {
	if len(args) == 0 {
		args = []string{"."}
	}
	res, err := scan.Scan(args)
	if err != nil {
		return Wrap(ExitGenericFailure, err)
	}
	if outputMode(cmd) == "json" {
		if err := emitJSON(cmd.OutOrStdout(), res); err != nil {
			return Wrap(ExitGenericFailure, err)
		}
	} else {
		printScanTable(cmd.OutOrStdout(), res)
	}
	if res.HasError() {
		return Errorf(ExitGenericFailure, "scan reported %d finding(s) at severity error", countErrors(res))
	}
	return nil
}

func printScanTable(w io.Writer, r *scan.Result) {
	if len(r.Findings) == 0 {
		fmt.Fprintln(w, "no findings")
		return
	}
	for _, f := range r.Findings {
		fmt.Fprintf(w, "[%s] %s:%d  %s — %s\n", f.Severity, f.File, f.Line, f.Rule, f.Message)
		if f.Snippet != "" {
			fmt.Fprintf(w, "    %s\n", f.Snippet)
		}
	}
}

func countErrors(r *scan.Result) int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == scan.SeverityError {
			n++
		}
	}
	return n
}
