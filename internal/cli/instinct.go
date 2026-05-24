package cli

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/forge/fdh/pkg/instincts"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// newInstinctCmd wires the `fdh instinct` subcommand tree.
//
// Subcommands: capture, list, show, edit, delete, export, import.
func newInstinctCmd(info BuildInfo) *cobra.Command {
	root := &cobra.Command{
		Use:   "instinct",
		Short: "Capture, share, and curate domain instincts",
		Long: `Manage local instincts under ~/.fdh/instincts/. An instinct is a small
YAML+markdown note that captures a domain pattern with a confidence score
and provenance. Devs capture their own; teams share via export/import;
admins run 'fdh evolve' to cluster instincts into skill drafts.

See docs/instincts.md for the full workflow and the format spec at
openspec/specs/instincts-format-and-storage/spec.md in the hub repo.`,
	}
	root.AddCommand(newInstinctCaptureCmd(info))
	root.AddCommand(newInstinctListCmd())
	root.AddCommand(newInstinctShowCmd())
	root.AddCommand(newInstinctEditCmd())
	root.AddCommand(newInstinctDeleteCmd())
	root.AddCommand(newInstinctExportCmd())
	root.AddCommand(newInstinctImportCmd())
	return root
}

// -----------------------------------------------------------------------------
// capture
// -----------------------------------------------------------------------------

func newInstinctCaptureCmd(info BuildInfo) *cobra.Command {
	var title, domain, body, bodyFile, tags string
	var confidence float64

	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Capture a new instinct (interactive in TTY, flag-driven otherwise)",
		Long: `Capture a new instinct. In a TTY without flags, prompts interactively
for title, domain, confidence, tags, and opens $EDITOR for the body.
With flags or non-TTY stdin, runs non-interactively.

Privacy: the body is whatever you write. Never paste secrets, API keys,
or proprietary data — this file lives in your home and may be shared
via 'fdh instinct export'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			interactive := title == "" && body == "" && bodyFile == "" && isTTY(os.Stdin)

			i := &instincts.Instinct{
				Confidence: confidence,
				Title:      title,
				Domain:     domain,
				CapturedAt: time.Now().UTC(),
			}
			if confidence == 0 {
				i.Confidence = 0.5 // sane default if neither flag nor prompt sets it
			}

			// Auto-populate captured_by from config or env.
			i.CapturedBy = resolveCapturedBy()

			// Auto-populate context from cwd + state.
			i.Context = inferContext()

			if interactive {
				if err := runCaptureWizard(cmd, i); err != nil {
					return err
				}
			} else {
				if err := finalizeFlagDriven(i, body, bodyFile, tags); err != nil {
					return err
				}
			}

			if i.ID == "" {
				id, err := instincts.NewULID(time.Now())
				if err != nil {
					return fmt.Errorf("generate id: %w", err)
				}
				i.ID = id
			}

			if err := i.Validate(); err != nil {
				return err
			}

			if err := instincts.WriteAtomic(i); err != nil {
				return fmt.Errorf("write instinct: %w", err)
			}

			// Bump state.
			now := time.Now().UTC()
			_ = instincts.MutateState(func(s *instincts.StateInstincts) {
				s.Count++
				s.LastCapture = &now
			})

			fmt.Fprintf(cmd.OutOrStdout(), "captured %s (%s)\n", i.ID, i.Title)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "Title (one-liner, ≤120 chars)")
	cmd.Flags().StringVar(&domain, "domain", "", "Domain (kebab-case, e.g. backend-services-go)")
	cmd.Flags().Float64Var(&confidence, "confidence", 0, "Confidence 0.0–1.0 (default 0.5)")
	cmd.Flags().StringVar(&tags, "tags", "", "Comma-separated tags")
	cmd.Flags().StringVar(&body, "body", "", "Body text (inline, short)")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "Path to a Markdown file with the body")
	_ = info
	return cmd
}

func runCaptureWizard(cmd *cobra.Command, i *instincts.Instinct) error {
	in := bufio.NewReader(os.Stdin)
	out := cmd.OutOrStdout()
	prompt := func(label, def string) (string, error) {
		if def != "" {
			fmt.Fprintf(out, "%s [%s]: ", label, def)
		} else {
			fmt.Fprintf(out, "%s: ", label)
		}
		line, err := in.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return def, nil
		}
		return line, nil
	}

	// Disclaimer.
	fmt.Fprintln(out, "─────────────────────────────────────────────────────────────────")
	fmt.Fprintln(out, "fdh instinct capture")
	fmt.Fprintln(out, "Note: never paste secrets, API keys, or proprietary data in the body —")
	fmt.Fprintln(out, "this file lives in your home and may be shared via export.")
	fmt.Fprintln(out, "─────────────────────────────────────────────────────────────────")

	var err error
	if i.Title, err = prompt("Title", ""); err != nil {
		return err
	}
	suggestedDomain := suggestDomain(i.Context)
	if i.Domain, err = prompt("Domain (kebab-case)", suggestedDomain); err != nil {
		return err
	}
	confStr, err := prompt("Confidence (0.0–1.0)", fmt.Sprintf("%.1f", i.Confidence))
	if err != nil {
		return err
	}
	if c, perr := strconv.ParseFloat(confStr, 64); perr == nil {
		i.Confidence = c
	}
	tagsStr, err := prompt("Tags (comma-separated)", "")
	if err != nil {
		return err
	}
	i.Tags = splitTags(tagsStr)

	// Body via $EDITOR.
	body, err := openEditorForBody(i.Title)
	if err != nil {
		return err
	}
	i.Body = body
	return nil
}

func finalizeFlagDriven(i *instincts.Instinct, body, bodyFile, tags string) error {
	if bodyFile != "" {
		data, err := os.ReadFile(bodyFile)
		if err != nil {
			return fmt.Errorf("read --body-file: %w", err)
		}
		i.Body = string(data)
	} else if body != "" {
		i.Body = body
	}
	i.Tags = splitTags(tags)
	return nil
}

func resolveCapturedBy() string {
	if v := os.Getenv("FDH_USER_EMAIL"); v != "" {
		return v
	}
	if v := viper.GetString("user.email"); v != "" {
		return v
	}
	// Last-resort placeholder; Validate will reject the empty.
	return os.Getenv("USER") + "@unknown.local"
}

func inferContext() instincts.Context {
	c := instincts.Context{}
	if cwd, err := os.Getwd(); err == nil {
		c.ProjectHint = filepath.Base(cwd)
	}
	// hub_commit best-effort from .fdh/lock.yaml if present.
	if cwd, err := os.Getwd(); err == nil {
		lockPath := filepath.Join(cwd, ".fdh", "lock.yaml")
		if data, err := os.ReadFile(lockPath); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "hub_commit:") {
					c.HubCommit = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "hub_commit:")), `"'`)
					break
				}
			}
		}
	}
	return c
}

func suggestDomain(c instincts.Context) string {
	// Naive: if there's a project_hint, suggest <hint>-domain.
	if c.ProjectHint != "" {
		return c.ProjectHint
	}
	return ""
}

func openEditorForBody(title string) (string, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		if runtime.GOOS == "windows" {
			editor = "notepad"
		} else {
			editor = "vi"
		}
	}
	tmp, err := os.CreateTemp("", "fdh-instinct-*.md")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	fmt.Fprintf(tmp, "<!--\nfdh instinct capture: %s\nWrite the body below this comment in Markdown. Save + close to continue.\n-->\n\n",
		title)
	_ = tmp.Close()

	c := exec.Command(editor, tmp.Name())
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("editor %q failed: %w", editor, err)
	}
	data, err := os.ReadFile(tmp.Name())
	if err != nil {
		return "", err
	}
	// Strip the leading comment block.
	s := string(data)
	if idx := strings.Index(s, "-->"); idx >= 0 {
		s = strings.TrimLeft(s[idx+3:], "\r\n")
	}
	return s, nil
}

func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// -----------------------------------------------------------------------------
// list
// -----------------------------------------------------------------------------

func newInstinctListCmd() *cobra.Command {
	var domain, tag, capturedBy, since, until string
	var confidenceMin float64
	var limit int
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List local instincts with optional filters",
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := loadFiltered(domain, tag, capturedBy, since, until, confidenceMin)
			if err != nil {
				return err
			}
			sort.Slice(items, func(i, j int) bool {
				return items[i].CapturedAt.After(items[j].CapturedAt)
			})
			if limit > 0 && len(items) > limit {
				items = items[:limit]
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(items)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tDOMAIN\tCONF\tCAPTURED_AT\tTITLE")
			for _, it := range items {
				fmt.Fprintf(w, "%s\t%s\t%.2f\t%s\t%s\n",
					it.ID[:8]+"…",
					it.Domain,
					it.Confidence,
					it.CapturedAt.UTC().Format("2006-01-02"),
					truncateForList(it.Title, 60),
				)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&domain, "domain", "", "Filter by domain (exact match)")
	cmd.Flags().StringVar(&tag, "tag", "", "Filter by tag (exact match)")
	cmd.Flags().StringVar(&capturedBy, "captured-by", "", "Filter by captured_by")
	cmd.Flags().StringVar(&since, "since", "", "Filter by captured_at >= (YYYY-MM-DD)")
	cmd.Flags().StringVar(&until, "until", "", "Filter by captured_at <= (YYYY-MM-DD)")
	cmd.Flags().Float64Var(&confidenceMin, "confidence-min", 0, "Filter by confidence >=")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum rows to show (0 = all)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit JSON array instead of a table")
	return cmd
}

func loadFiltered(domain, tag, capturedBy, since, until string, confMin float64) ([]*instincts.Instinct, error) {
	all, err := instincts.ReadAll()
	if err != nil {
		return nil, err
	}
	var sinceT, untilT time.Time
	if since != "" {
		t, err := time.Parse("2006-01-02", since)
		if err != nil {
			return nil, fmt.Errorf("--since: %w", err)
		}
		sinceT = t
	}
	if until != "" {
		t, err := time.Parse("2006-01-02", until)
		if err != nil {
			return nil, fmt.Errorf("--until: %w", err)
		}
		untilT = t.Add(24 * time.Hour)
	}
	out := make([]*instincts.Instinct, 0, len(all))
	for _, it := range all {
		if domain != "" && it.Domain != domain {
			continue
		}
		if capturedBy != "" && it.CapturedBy != capturedBy {
			continue
		}
		if confMin > 0 && it.Confidence < confMin {
			continue
		}
		if !sinceT.IsZero() && it.CapturedAt.Before(sinceT) {
			continue
		}
		if !untilT.IsZero() && it.CapturedAt.After(untilT) {
			continue
		}
		if tag != "" {
			found := false
			for _, t := range it.Tags {
				if strings.EqualFold(t, tag) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		out = append(out, it)
	}
	return out, nil
}

func truncateForList(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// -----------------------------------------------------------------------------
// show / edit / delete
// -----------------------------------------------------------------------------

func newInstinctShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id-prefix>",
		Short: "Print one instinct's YAML file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := instincts.ResolvePrefix(args[0])
			if err != nil {
				return err
			}
			p, _ := instincts.Path(id)
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	}
}

func newInstinctEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit <id-prefix>",
		Short: "Open the instinct in $EDITOR; re-validates on save",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := instincts.ResolvePrefix(args[0])
			if err != nil {
				return err
			}
			p, _ := instincts.Path(id)
			editor := os.Getenv("EDITOR")
			if editor == "" {
				if runtime.GOOS == "windows" {
					editor = "notepad"
				} else {
					editor = "vi"
				}
			}
			c := exec.Command(editor, p)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				return fmt.Errorf("editor %q failed: %w", editor, err)
			}
			// Re-validate.
			loaded, err := instincts.Read(id)
			if err != nil {
				return fmt.Errorf("re-read after edit: %w", err)
			}
			if err := loaded.Validate(); err != nil {
				return fmt.Errorf("post-edit validation failed: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "saved %s\n", id)
			return nil
		},
	}
}

func newInstinctDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id-prefix>",
		Short: "Delete an instinct (prompts unless --yes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := instincts.ResolvePrefix(args[0])
			if err != nil {
				return err
			}
			i, err := instincts.Read(id)
			if err != nil {
				return err
			}
			if !yes {
				if !isTTY(os.Stdin) {
					return fmt.Errorf("non-interactive: pass --yes to confirm deletion of %s", id)
				}
				fmt.Fprintf(cmd.OutOrStdout(), `Delete instinct "%s"? [y/N] `, i.Title)
				reply, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				reply = strings.TrimSpace(strings.ToLower(reply))
				if reply != "y" && reply != "yes" {
					fmt.Fprintln(cmd.OutOrStdout(), "aborted")
					return nil
				}
			}
			if err := instincts.Delete(id); err != nil {
				return err
			}
			_ = instincts.MutateState(func(s *instincts.StateInstincts) {
				if s.Count > 0 {
					s.Count--
				}
			})
			fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", id)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Do not prompt for confirmation")
	return cmd
}

// -----------------------------------------------------------------------------
// export
// -----------------------------------------------------------------------------

func newInstinctExportCmd() *cobra.Command {
	var domain, tag, capturedBy, since, until string
	var confidenceMin float64
	var all, noScan bool

	cmd := &cobra.Command{
		Use:   "export <output-file>",
		Short: "Bundle instincts (.yaml / .json / .tar.gz) for sharing",
		Long: `Exports a bundle in YAML, JSON, or tar.gz based on the output extension.
Before writing, runs 'fdh scan' over the bundle to catch secrets/PII;
pass --no-scan to skip with an explicit warning.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			outPath := args[0]
			format := instincts.DetectBundleFormat(outPath)
			if format == instincts.BundleUnknown {
				return fmt.Errorf("unknown bundle format for %s; use .yaml/.json/.tar.gz", outPath)
			}
			var items []*instincts.Instinct
			var err error
			if all {
				items, err = instincts.ReadAll()
			} else {
				items, err = loadFiltered(domain, tag, capturedBy, since, until, confidenceMin)
			}
			if err != nil {
				return err
			}
			if len(items) == 0 {
				return fmt.Errorf("no instincts matched the filters; nothing to export")
			}

			if !noScan {
				if findings, err := runFdhScanOnBundle(items); err != nil {
					return fmt.Errorf("scan failed: %w (use --no-scan to skip with caution)", err)
				} else if len(findings) > 0 {
					fmt.Fprintln(cmd.ErrOrStderr(), "export blocked by scan; findings:")
					for _, f := range findings {
						fmt.Fprintln(cmd.ErrOrStderr(), "  - "+f)
					}
					return fmt.Errorf("aborting export; fix or use --no-scan to override")
				}
			} else {
				fmt.Fprintln(cmd.ErrOrStderr(), "WARNING: --no-scan; this bundle may contain sensitive data")
			}

			f, err := os.Create(outPath)
			if err != nil {
				return err
			}
			defer f.Close()
			if format == instincts.BundleTarGz {
				if err := exportTarGz(f, items); err != nil {
					return err
				}
			} else {
				if err := instincts.WriteAll(f, format, items); err != nil {
					return err
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "exported %d instincts to %s\n", len(items), outPath)

			now := time.Now().UTC()
			_ = instincts.MutateState(func(s *instincts.StateInstincts) {
				s.LastExport = &now
			})
			return nil
		},
	}
	cmd.Flags().StringVar(&domain, "domain", "", "Filter by domain")
	cmd.Flags().StringVar(&tag, "tag", "", "Filter by tag")
	cmd.Flags().StringVar(&capturedBy, "captured-by", "", "Filter by captured_by")
	cmd.Flags().StringVar(&since, "since", "", "Filter by captured_at >= (YYYY-MM-DD)")
	cmd.Flags().StringVar(&until, "until", "", "Filter by captured_at <= (YYYY-MM-DD)")
	cmd.Flags().Float64Var(&confidenceMin, "confidence-min", 0, "Filter by confidence >=")
	cmd.Flags().BoolVar(&all, "all", false, "Export every local instinct (overrides filters)")
	cmd.Flags().BoolVar(&noScan, "no-scan", false, "Skip the pre-export scan (use with caution)")
	return cmd
}

func exportTarGz(w io.Writer, items []*instincts.Instinct) error {
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	for _, it := range items {
		encoded, err := it.Encode()
		if err != nil {
			return fmt.Errorf("encode %s: %w", it.ID, err)
		}
		hdr := &tar.Header{
			Name:    "instincts/" + it.ID + ".yaml",
			Size:    int64(len(encoded)),
			Mode:    0o600,
			ModTime: it.CapturedAt.UTC(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(encoded); err != nil {
			return err
		}
	}
	return nil
}

// runFdhScanOnBundle invokes `fdh scan` on each instinct body for secrets and
// returns a list of human-readable finding lines. If `fdh scan` is not yet
// implemented in this build, returns no findings with a stderr note (the spec
// allows for v1-minimal scan to land alongside; pre-scan presence is best-effort).
//
// To keep this commit self-contained while fdh-scan-security lands in the
// sibling change, the implementation here does a built-in minimum: regex
// secrets detection. Replace by shelling out to `fdh scan` once available.
func runFdhScanOnBundle(items []*instincts.Instinct) ([]string, error) {
	var findings []string
	for _, it := range items {
		for _, finding := range minimalSecretsScan(it.Body) {
			findings = append(findings, fmt.Sprintf("%s: %s", it.ID[:8]+"…", finding))
		}
	}
	return findings, nil
}

// minimalSecretsScan is a stop-gap until fdh-scan-security ships. It looks for
// the most catastrophic patterns: AWS access keys, GitHub tokens, JWTs.
var (
	awsKey    = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
	ghToken   = regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`)
	jwtToken  = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)
	urlCreds  = regexp.MustCompile(`https?://[^:/\s]+:[^@/\s]+@`)
)

func minimalSecretsScan(body string) []string {
	var out []string
	if loc := awsKey.FindStringIndex(body); loc != nil {
		out = append(out, "looks like AWS access key (AKIA…) — line "+lineOf(body, loc[0]))
	}
	if loc := ghToken.FindStringIndex(body); loc != nil {
		out = append(out, "looks like a GitHub token (gh[pousr]_…) — line "+lineOf(body, loc[0]))
	}
	if loc := jwtToken.FindStringIndex(body); loc != nil {
		out = append(out, "looks like a JWT — line "+lineOf(body, loc[0]))
	}
	if loc := urlCreds.FindStringIndex(body); loc != nil {
		out = append(out, "URL with embedded credentials — line "+lineOf(body, loc[0]))
	}
	return out
}

func lineOf(s string, byteOffset int) string {
	line := 1
	for i := 0; i < byteOffset && i < len(s); i++ {
		if s[i] == '\n' {
			line++
		}
	}
	return strconv.Itoa(line)
}

// -----------------------------------------------------------------------------
// import
// -----------------------------------------------------------------------------

func newInstinctImportCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Import an instincts bundle (yaml/json/tar.gz) with dedup",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			format := instincts.DetectBundleFormat(path)
			if format == instincts.BundleUnknown {
				return fmt.Errorf("unknown bundle format for %s; use .yaml/.json/.tar.gz", path)
			}
			items, err := readBundle(path, format)
			if err != nil {
				return err
			}

			// Build hash index of local instincts for dedup.
			local, err := instincts.ReadAll()
			if err != nil {
				return err
			}
			localHashes := map[string]string{} // hash -> ID
			localIDs := map[string]*instincts.Instinct{}
			for _, l := range local {
				localHashes[l.BodyHash()] = l.ID
				localIDs[l.ID] = l
			}

			var imported, duplicates, malformed int
			for _, in := range items {
				if err := in.Validate(); err != nil {
					malformed++
					fmt.Fprintf(cmd.ErrOrStderr(), "  malformed: %s — %v\n", in.ID, err)
					continue
				}
				if existing, ok := localIDs[in.ID]; ok {
					if existing.BodyHash() == in.BodyHash() {
						duplicates++
						continue
					}
					return fmt.Errorf("conflict: instinct %s already exists locally with different body; resolve manually", in.ID)
				}
				if id, dup := localHashes[in.BodyHash()]; dup {
					duplicates++
					fmt.Fprintf(cmd.ErrOrStderr(), "  duplicate body of %s (skipping %s)\n", id, in.ID)
					continue
				}
				if dryRun {
					imported++
					continue
				}
				if err := instincts.WriteAtomic(in); err != nil {
					return fmt.Errorf("write %s: %w", in.ID, err)
				}
				imported++
			}

			if !dryRun {
				_ = instincts.MutateState(func(s *instincts.StateInstincts) {
					s.Count += imported
				})
			}
			mode := ""
			if dryRun {
				mode = " (dry-run)"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "import%s: imported=%d, duplicates=%d, malformed=%d\n",
				mode, imported, duplicates, malformed)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview without writing to disk")
	return cmd
}

func readBundle(path string, format instincts.BundleFormat) ([]*instincts.Instinct, error) {
	if format == instincts.BundleTarGz {
		return readBundleTarGz(path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return instincts.ReadAllBundle(data, format)
}

func readBundleTarGz(path string) ([]*instincts.Instinct, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var out []*instincts.Instinct
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if !strings.HasSuffix(hdr.Name, ".yaml") {
			continue
		}
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, tr); err != nil {
			return nil, err
		}
		i, err := instincts.Decode(buf.Bytes())
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", hdr.Name, err)
		}
		out = append(out, i)
	}
	return out, nil
}
