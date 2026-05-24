package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// defaultPilotRegistry is the registry source the `init` command suggests
// when none has been configured yet AND no flag/env override was given.
// forge's production registry URL is set via FDH_DEFAULT_REGISTRY at
// distribution time (Helm values, package-manager wrapper script, etc.);
// the hard-coded fallback below points at the Git remote the platform
// team intends to provision. Edit it when the real URL is confirmed.
const defaultPilotRegistry = "https://git.forge.internal/skills/registry.git"

// InitResult is the JSON shape emitted by `init --json`.
//
// Fields are additive-only per the fdh-cli-implementation-contract
// spec: never rename, never change type, never change semantics.
// New optional fields may be appended.
type InitResult struct {
	ConfigPath    string            `json:"config_path"`
	Applied       map[string]string `json:"applied"`
	Existing      map[string]string `json:"existing,omitempty"`
	DoctorOK      bool              `json:"doctor_ok"`
	DoctorSummary string            `json:"doctor_summary,omitempty"`

	// SelectedAgents lists the agent IDs the wizard chose. Omitted
	// when the wizard was skipped (no flags + non-TTY or
	// --skip-wizard).
	SelectedAgents []string `json:"selected_agents,omitempty"`

	// SelectedSkills lists the skill names the wizard chose. Omitted
	// in the same conditions as SelectedAgents.
	SelectedSkills []string `json:"selected_skills,omitempty"`

	// InstalledSkills records one entry per (skill, agent) pair the
	// install loop attempted. Empty when the wizard was skipped.
	InstalledSkills []InstalledSkillResult `json:"installed_skills,omitempty"`

	// WizardSkipped explains why the wizard did not run, when that
	// condition is informational (e.g. non-TTY + non-interactive).
	// Empty when the wizard ran.
	WizardSkipped string `json:"wizard_skipped,omitempty"`
}

func newInitCmd(info BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "First-run setup: configure registry + scope, then run doctor",
		Long: `Configure fdh in one command, with sensible defaults.

` + "`fdh init`" + ` writes (or updates) ~/.config/fdh/config.yaml with the
registry source and the default install scope, then runs ` + "`fdh doctor`" + `
to verify everything is reachable.

Without flags, init uses:
  - FDH_DEFAULT_REGISTRY env var if set, else a built-in pilot default
  - defaults.scope = auto (project when .git/ is detectable, else user)

Flags override the auto-picked values. Run as many times as needed —
it is idempotent and never destroys existing settings unless explicitly
asked to via --force.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, info)
		},
	}
	cmd.Flags().String("registry-url", "", "remote URL of the skill registry (Git)")
	cmd.Flags().String("registry-local", "", "absolute path to a local clone of the skill registry")
	cmd.Flags().String("scope", "", "default install scope: user | project | auto")
	cmd.Flags().Bool("skip-doctor", false, "do not run doctor after configuring")
	cmd.Flags().Bool("force", false, "overwrite existing values (default: keep existing, only fill missing)")

	// Wizard flags (Section 4 of implement-cli-distribution-and-interactive-init).
	cmd.Flags().StringSlice("agents", nil, "non-interactive: install to these agent IDs (comma-separated)")
	cmd.Flags().StringSlice("skills", nil, "non-interactive: install these skills (comma-separated)")
	cmd.Flags().Bool("no-defaults", false, "do not pre-select default skills")
	cmd.Flags().Bool("non-interactive", false, "never run the wizard; require --agents/--skills")
	cmd.Flags().Bool("dry-run", false, "compute the install plan without writing")
	return cmd
}

func runInit(cmd *cobra.Command, info BuildInfo) error {
	registryURL, _ := cmd.Flags().GetString("registry-url")
	registryLocal, _ := cmd.Flags().GetString("registry-local")
	scope, _ := cmd.Flags().GetString("scope")
	skipDoctor, _ := cmd.Flags().GetBool("skip-doctor")
	force, _ := cmd.Flags().GetBool("force")
	agentsFlag, _ := cmd.Flags().GetStringSlice("agents")
	skillsFlag, _ := cmd.Flags().GetStringSlice("skills")
	noDefaults, _ := cmd.Flags().GetBool("no-defaults")
	nonInteractive, _ := cmd.Flags().GetBool("non-interactive")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	if registryURL != "" && registryLocal != "" {
		return Errorf(ExitInvalidUsage,
			"--registry-url and --registry-local are mutually exclusive")
	}

	// Decide what to apply. Existing values are kept unless --force is set.
	type kv struct{ key, value string }
	var plan []kv
	existing := map[string]string{}

	currentURL := viper.GetString("registry.url")
	currentLocal := viper.GetString("registry.local_path")
	currentScope := viper.GetString("defaults.scope")

	if registryURL != "" {
		plan = append(plan, kv{"registry.url", registryURL})
		if registryLocal == "" {
			plan = append(plan, kv{"registry.local_path", ""}) // clear conflicting
		}
	} else if registryLocal != "" {
		plan = append(plan, kv{"registry.local_path", registryLocal})
		if registryURL == "" {
			plan = append(plan, kv{"registry.url", ""}) // clear conflicting
		}
	} else if currentURL == "" && currentLocal == "" {
		// Nothing configured + no flag — apply the built-in pilot default.
		def := defaultRegistryFromEnv()
		plan = append(plan, kv{"registry.url", def})
	}

	if scope != "" {
		if !isValidScope(scope) {
			return Errorf(ExitInvalidUsage,
				"invalid --scope %q (expected user|project|auto)", scope)
		}
		plan = append(plan, kv{"defaults.scope", scope})
	} else if currentScope == "" {
		plan = append(plan, kv{"defaults.scope", "auto"})
	}

	// Apply the plan respecting --force.
	applied := map[string]string{}
	for _, item := range plan {
		current := viper.GetString(item.key)
		if current != "" && !force {
			existing[item.key] = current
			continue
		}
		viper.Set(item.key, item.value)
		applied[item.key] = item.value
	}

	if err := writeConfigFile(); err != nil {
		return Wrap(ExitPermission, fmt.Errorf("persist config: %w", err))
	}

	result := InitResult{
		ConfigPath: configFilePath(),
		Applied:    applied,
		Existing:   existing,
	}

	// Wizard dispatch. The wizard activates when (1) a registry is
	// configured (so we know where to load the hub catalog from),
	// AND (2) either we're in a TTY without --non-interactive, or
	// the caller passed --agents/--skills explicitly.
	wizardRequested := len(agentsFlag) > 0 || len(skillsFlag) > 0 || noDefaults
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rc, rcErr := buildRunContext(ctx, info, false)
	hubURL := ""
	if rcErr == nil {
		hubURL = hubURLFromConfigOrFlag(rc)
	}

	switch {
	case rcErr != nil:
		result.WizardSkipped = "run context unavailable: " + rcErr.Error()
	case hubURL == "":
		result.WizardSkipped = "no registry URL configured (set --registry-url or FDH_DEFAULT_REGISTRY)"
	case nonInteractive && !wizardRequested:
		result.WizardSkipped = "non-interactive without --agents/--skills"
	case !nonInteractive && !stdinIsTTY() && !wizardRequested:
		// Per the contract: when stdin isn't a TTY and no flags were
		// passed, emit an informational message and skip the wizard
		// without erroring (exit 0).
		fmt.Fprintln(cmd.OutOrStderr(),
			"wizard requires a TTY; use --agents / --skills flags or --non-interactive")
		result.WizardSkipped = "stdin is not a TTY"
	default:
		var prompter wizardPrompter
		if !nonInteractive && stdinIsTTY() && !wizardRequested {
			prompter = huhPrompter{in: os.Stdin, out: cmd.OutOrStderr()}
		}
		input := wizardInput{Agents: agentsFlag, Skills: skillsFlag}
		selAgents, selSkills, installed, err := runInitWizard(
			ctx, cmd.OutOrStdout(), cmd.OutOrStderr(),
			rc, hubURL, prompter, input,
			noDefaults, dryRun, info.Version,
		)
		if err != nil {
			return err
		}
		result.SelectedAgents = selAgents
		result.SelectedSkills = selSkills
		result.InstalledSkills = installed
	}

	// Optionally run doctor so the user gets immediate validation.
	if !skipDoctor {
		ok, summary := runDoctorSilent(info)
		result.DoctorOK = ok
		result.DoctorSummary = summary
	} else {
		result.DoctorOK = true // not asked, so report success
		result.DoctorSummary = "doctor skipped (--skip-doctor)"
	}

	if outputMode(cmd) == "json" {
		return emitJSON(cmd.OutOrStdout(), result)
	}
	printInitSummary(cmd.OutOrStdout(), result, skipDoctor)

	if !result.DoctorOK {
		return Errorf(ExitGenericFailure, "doctor reported errors after init — see above")
	}
	return nil
}

func defaultRegistryFromEnv() string {
	if v := strings.TrimSpace(envVar("FDH_DEFAULT_REGISTRY")); v != "" {
		return v
	}
	return defaultPilotRegistry
}

func isValidScope(s string) bool {
	switch strings.ToLower(s) {
	case "user", "project", "auto":
		return true
	}
	return false
}

func configFilePath() string {
	dir := defaultConfigDir()
	if dir == "" {
		return "(unknown)"
	}
	return filepath.Join(dir, "config.yaml")
}

// runDoctorSilent invokes the doctor pipeline without printing its full
// report. Returns (ok, one-line summary). Used by init so the user sees
// init's own summary plus a green/red line, not the full doctor output.
func runDoctorSilent(info BuildInfo) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rc, err := buildRunContext(ctx, info, false)
	if err != nil {
		return false, "could not build run context: " + err.Error()
	}
	if rc.Registry == nil {
		return false, "no registry configured"
	}
	if _, err := rc.Registry.Index(rc.Ctx); err != nil {
		return false, "registry unreachable: " + err.Error()
	}
	detected := 0
	for _, d := range rc.Adapters.DetectAll(probeContextFor(rc)) {
		if d.Detected {
			detected++
		}
	}
	if detected == 0 {
		return false, "no AI agents detected on this host"
	}
	return true, fmt.Sprintf("registry reachable, %d agent(s) detected", detected)
}

func printInitSummary(w io.Writer, r InitResult, skipDoctor bool) {
	fmt.Fprintln(w, "fdh init")
	fmt.Fprintf(w, "  config:  %s\n", r.ConfigPath)
	if len(r.Applied) > 0 {
		fmt.Fprintln(w, "  applied:")
		for k, v := range r.Applied {
			fmt.Fprintf(w, "    %s = %s\n", k, displayValue(v))
		}
	}
	if len(r.Existing) > 0 {
		fmt.Fprintln(w, "  kept (pass --force to overwrite):")
		for k, v := range r.Existing {
			fmt.Fprintf(w, "    %s = %s\n", k, displayValue(v))
		}
	}
	if skipDoctor {
		fmt.Fprintln(w, "  doctor:  skipped")
	} else if r.DoctorOK {
		fmt.Fprintf(w, "  doctor:  OK (%s)\n", r.DoctorSummary)
	} else {
		fmt.Fprintf(w, "  doctor:  PROBLEM (%s)\n", r.DoctorSummary)
	}

	if r.DoctorOK && !skipDoctor {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Next steps:")
		fmt.Fprintln(w, "  fdh search <query>          # browse available skills")
		fmt.Fprintln(w, "  fdh install <ns>/<name>     # install one to all detected agents")
	}
}

func displayValue(v string) string {
	if v == "" {
		return "(cleared)"
	}
	return v
}
