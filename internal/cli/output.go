package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// outputMode reports whether --json is set on this invocation.
func outputMode(cmd *cobra.Command) string {
	useJSON, _ := cmd.Flags().GetBool("json")
	if useJSON {
		return "json"
	}
	return "table"
}

// emitJSON writes v as pretty-printed JSON to w.
func emitJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// printTable writes a simple aligned-column table to w.
//
// headers is the column titles; rows is one row per record where each cell
// is rendered with %s formatting (use fmt.Sprintf at the call site to format
// numbers and slices). Empty cells become an empty string.
func printTable(w io.Writer, headers []string, rows [][]string) error {
	cols := len(headers)
	widths := make([]int, cols)
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, r := range rows {
		for i := 0; i < cols && i < len(r); i++ {
			if len(r[i]) > widths[i] {
				widths[i] = len(r[i])
			}
		}
	}
	// Header row.
	if err := writeRow(w, headers, widths); err != nil {
		return err
	}
	// Separator.
	parts := make([]string, cols)
	for i, ww := range widths {
		parts[i] = strings.Repeat("-", ww)
	}
	if err := writeRow(w, parts, widths); err != nil {
		return err
	}
	// Data rows.
	for _, r := range rows {
		if err := writeRow(w, r, widths); err != nil {
			return err
		}
	}
	return nil
}

func writeRow(w io.Writer, cells []string, widths []int) error {
	for i, ww := range widths {
		val := ""
		if i < len(cells) {
			val = cells[i]
		}
		if i > 0 {
			if _, err := fmt.Fprint(w, "  "); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "%-*s", ww, val); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

// joinAgents joins a sorted agent ID list for table display.
func joinAgents(ids []string) string {
	if len(ids) == 0 {
		return "-"
	}
	return strings.Join(ids, ",")
}
