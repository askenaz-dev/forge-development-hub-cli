package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// newFeedbackCmd implements `fdh feedback` — the voluntary (Tier 2) channel.
// It is the only path that transmits free-form text, and only because the user
// explicitly typed it. Sentiment is required; text is optional.
func newFeedbackCmd(info BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "feedback <component> --up|--down [--message <text>]",
		Short: "Send voluntary feedback about a component to the hub",
		Long: `Send a thumbs-up or thumbs-down plus an optional short message about a
component. This is voluntary and explicit: nothing is sent unless you run this
command. Telemetry must be enabled (it is on by default; see
'fdh config telemetry').

The component reference is "<namespace>/<name>" or just "<name>".`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFeedback(cmd, args, info)
		},
	}
	cmd.Flags().Bool("up", false, "positive feedback (👍)")
	cmd.Flags().Bool("down", false, "negative feedback (👎)")
	cmd.Flags().String("message", "", "optional short free-text message")
	cmd.Flags().String("kind", "skill", "component kind: skill|rule|agent|hook")
	return cmd
}

func runFeedback(cmd *cobra.Command, args []string, info BuildInfo) error {
	up, _ := cmd.Flags().GetBool("up")
	down, _ := cmd.Flags().GetBool("down")
	if up == down { // both or neither
		return Errorf(ExitInvalidUsage, "exactly one of --up or --down is required")
	}
	if !telemetryEnabled() {
		return Errorf(ExitInvalidUsage, "telemetry is disabled; enable it with 'fdh config telemetry on' to send feedback")
	}
	if telemetryEndpoint() == "" {
		return Errorf(ExitInvalidUsage, "no hub endpoint configured; set registry.url or telemetry.endpoint first")
	}

	ref := args[0]
	namespace, name := "", ref
	if i := strings.IndexByte(ref, '/'); i >= 0 {
		namespace, name = ref[:i], ref[i+1:]
	}
	sentiment := "up"
	if down {
		sentiment = "down"
	}
	kind, _ := cmd.Flags().GetString("kind")
	message, _ := cmd.Flags().GetString("message")

	attrs := map[string]string{
		"kind": kind, "name": name, "sentiment": sentiment, "surface": "cli",
	}
	if namespace != "" {
		attrs["namespace"] = namespace
	}
	if strings.TrimSpace(message) != "" {
		attrs["text"] = message
	}
	emitTelemetry(cmd, EventFeedbackName, attrs)
	fmt.Fprintf(cmd.OutOrStdout(), "Thanks! Recorded %s feedback for %s.\n", sentiment, ref)
	return nil
}
