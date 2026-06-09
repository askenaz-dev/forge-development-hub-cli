package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/forge/fdh/internal/cli/telemetry"
)

// newTelemetryCmd registers `fdh telemetry status|enable|disable|rotate`.
// All output is English (project convention).
func newTelemetryCmd(info BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telemetry",
		Short: "Inspect and control anonymous, opt-in usage telemetry",
		Long: `Anonymous, pseudonymous usage telemetry is OFF by default.

Telemetry is fully reversible and never collects personally identifying
information. When enabled, the CLI sends a coarse event (install/download/
resolve) with the component's kind/namespace/name/version/content-hash,
scope, registry, a coarse OS bucket, locale, and a pseudonymous rotating
install id — and nothing else.

Privacy policy: ` + telemetry.PrivacyPolicyURL,
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show whether telemetry is enabled and why",
		RunE:  func(cmd *cobra.Command, args []string) error { return runTelemetryStatus(cmd) },
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "enable",
		Short: "Opt in to anonymous usage telemetry",
		RunE:  func(cmd *cobra.Command, args []string) error { return runTelemetrySet(cmd, true) },
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "disable",
		Short: "Opt out of usage telemetry (durable)",
		RunE:  func(cmd *cobra.Command, args []string) error { return runTelemetrySet(cmd, false) },
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "rotate",
		Short: "Rotate the pseudonymous install id (right-to-be-forgotten)",
		RunE:  func(cmd *cobra.Command, args []string) error { return runTelemetryRotate(cmd) },
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "claim",
		Short: "Print this machine's claim code to link its installs to your portal profile",
		Long: `Print this machine's pseudonymous install id as a CLAIM CODE.

Telemetry is anonymous: the platform never reverses your install id to your
identity. If you WANT your installs from this machine to appear in your portal
profile's activity feed, copy the claim code below and paste it into your
profile. This is the only way installs are linked to you — it is voluntary and
you initiate it. Requires telemetry to be enabled (so an install id exists).`,
		RunE: func(cmd *cobra.Command, args []string) error { return runTelemetryClaim(cmd) },
	})
	return cmd
}

func runTelemetryStatus(cmd *cobra.Command) error {
	mgr := telemetry.NewManager(defaultConfigDir())
	configEnabled := strings.TrimSpace(viper.GetString("telemetry.enabled"))
	d := mgr.ResolveWithConsent(configEnabled, os.Getenv)

	state := "disabled"
	if d.Enabled {
		state = "enabled"
	}

	if outputMode(cmd) == "json" {
		out := map[string]any{
			"state":          state,
			"enabled":        d.Enabled,
			"deciding_input": string(d.Source),
			"privacy_policy": telemetry.PrivacyPolicyURL,
		}
		return emitJSON(cmd.OutOrStdout(), out)
	}

	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "telemetry: %s\n", state)
	fmt.Fprintf(w, "  decided by: %s\n", d.Source)
	fmt.Fprintf(w, "  privacy policy: %s\n", telemetry.PrivacyPolicyURL)
	if d.Enabled {
		// Only surface the (pseudonymous) id when enabled; never materialize
		// a salt for a disabled user.
		if id, err := mgr.InstallID(); err == nil {
			fmt.Fprintf(w, "  install id: %s\n", id)
		}
	}
	fmt.Fprintln(w, "  manage: fdh telemetry enable | disable | rotate")
	return nil
}

// runTelemetrySet writes the durable telemetry.enabled config key AND records
// the consent answer, so the two never diverge and the first-run prompt never
// recurs after an explicit choice.
func runTelemetrySet(cmd *cobra.Command, enabled bool) error {
	viper.Set("telemetry.enabled", strconv.FormatBool(enabled))
	if err := writeConfigFile(); err != nil {
		return Wrap(ExitPermission, fmt.Errorf("persist telemetry config: %w", err))
	}
	mgr := telemetry.NewManager(defaultConfigDir())
	// Best-effort: a failure to record consent does not undo the config write.
	_ = mgr.SetEnabledConsent(enabled)

	w := cmd.OutOrStdout()
	if enabled {
		fmt.Fprintln(w, "Anonymous telemetry enabled. Thank you — this helps improve the platform.")
		fmt.Fprintf(w, "Disable any time with 'fdh telemetry disable'. Policy: %s\n", telemetry.PrivacyPolicyURL)
	} else {
		fmt.Fprintln(w, "Telemetry disabled. No usage events will be sent.")
	}
	return nil
}

func runTelemetryRotate(cmd *cobra.Command) error {
	mgr := telemetry.NewManager(defaultConfigDir())
	if err := mgr.Rotate(); err != nil {
		return Wrap(ExitPermission, fmt.Errorf("rotate telemetry id: %w", err))
	}
	fmt.Fprintln(cmd.OutOrStdout(),
		"Rotated the pseudonymous install id. The previous id can no longer be associated with this machine.")
	return nil
}

// runTelemetryClaim prints the pseudonymous install id as the claim code the
// user pastes into their portal profile to VOLUNTARILY link this machine's
// installs to their activity feed (design D5). It refuses when telemetry is
// disabled — no install id exists to claim, and we never materialize one for a
// disabled user. Output is the claim code on its own line (plus a one-line
// hint), in English.
func runTelemetryClaim(cmd *cobra.Command) error {
	mgr := telemetry.NewManager(defaultConfigDir())
	configEnabled := strings.TrimSpace(viper.GetString("telemetry.enabled"))
	d := mgr.ResolveWithConsent(configEnabled, os.Getenv)
	if !d.Enabled {
		return Errorf(ExitInvalidUsage,
			"telemetry is disabled, so there is no install id to claim; run 'fdh telemetry enable' first")
	}
	id, err := mgr.InstallID()
	if err != nil {
		return Wrap(ExitPermission, fmt.Errorf("read install id: %w", err))
	}

	if outputMode(cmd) == "json" {
		return emitJSON(cmd.OutOrStdout(), map[string]any{"claim_code": id})
	}

	w := cmd.OutOrStdout()
	fmt.Fprintln(w, id)
	fmt.Fprintln(w, "Paste this claim code into your portal profile to link this machine's installs to your activity feed.")
	return nil
}

// newFeedbackCmd registers `fdh feedback`: a structured rating/category plus
// free-text submission POSTed as event=feedback to the anonymous ingest
// endpoint. Submission is best-effort and anonymous (it carries no identity).
func newFeedbackCmd(info BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "feedback",
		Short: "Send anonymous feedback (a rating, a category, and a comment) to the platform team",
		Long: `Submit anonymous feedback to the Forge platform team.

You can pass --rating, --category, and --message non-interactively, or run
the command on a terminal to be prompted. Feedback is sent anonymously to
the portal's feedback ingest and carries no identity. It does not require
telemetry to be enabled.`,
		RunE: func(cmd *cobra.Command, args []string) error { return runFeedback(cmd) },
	}
	cmd.Flags().Int("rating", 0, "satisfaction rating from 1 (poor) to 5 (great); 0 = unset")
	cmd.Flags().String("category", "", "feedback category, e.g. bug|idea|docs|other")
	cmd.Flags().String("message", "", "free-text comment")
	return cmd
}

func runFeedback(cmd *cobra.Command) error {
	rating, _ := cmd.Flags().GetInt("rating")
	category, _ := cmd.Flags().GetString("category")
	message, _ := cmd.Flags().GetString("message")

	// Prompt interactively for any missing field when on a TTY.
	if stdinIsTTY() {
		if rating == 0 {
			rating = promptRating(cmd)
		}
		if category == "" {
			category = promptLine(cmd, "Category [bug/idea/docs/other] (optional): ")
		}
		if message == "" {
			message = promptLine(cmd, "Comment (optional): ")
		}
	}

	if rating == 0 && strings.TrimSpace(category) == "" && strings.TrimSpace(message) == "" {
		return Errorf(ExitInvalidUsage,
			"nothing to send: provide at least one of --rating, --category, or --message")
	}
	if rating < 0 || rating > 5 {
		return Errorf(ExitInvalidUsage, "rating must be between 1 and 5 (got %d)", rating)
	}

	endpoint := strings.TrimSpace(viper.GetString("telemetry.endpoint"))
	if endpoint == "" {
		endpoint = telemetry.DefaultEndpoint
	}

	// Feedback is anonymous and always enabled (it is an explicit user
	// action, not passive telemetry). It carries NO install-id or identity.
	em := telemetry.NewEmitter(endpoint, true)
	em.Enqueue(telemetry.Event{
		Event:     "feedback",
		OS:        telemetry.CoarseOS(),
		Locale:    currentLocale(),
		Timestamp: nowRFC3339(),
		Rating:    rating,
		Category:  strings.TrimSpace(category),
		Text:      strings.TrimSpace(message),
	})
	// Best-effort flush, time-boxed by the emitter; never blocks beyond it.
	wait := em.FlushAsync(context.Background())
	wait()

	fmt.Fprintln(cmd.OutOrStdout(), "Thanks for the feedback!")
	return nil
}

func promptRating(cmd *cobra.Command) int {
	for {
		s := promptLine(cmd, "Rating 1-5 (Enter to skip): ")
		if s == "" {
			return 0
		}
		n, err := strconv.Atoi(s)
		if err == nil && n >= 1 && n <= 5 {
			return n
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "Please enter a number from 1 to 5.")
	}
}

func promptLine(cmd *cobra.Command, label string) string {
	fmt.Fprint(cmd.OutOrStdout(), label)
	r := bufio.NewReader(cmd.InOrStdin())
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(line)
}
