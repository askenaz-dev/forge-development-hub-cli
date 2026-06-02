// Package cli wires the root cobra command and its subcommands.
package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// BuildInfo carries the version metadata stamped at link time.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

// Execute builds the root command tree and runs it. It returns nil on success
// or a cliError on failure; main.go uses ExitCode to map errors to exit codes.
func Execute(info BuildInfo) error {
	root := newRootCmd(info)
	return root.Execute()
}

func newRootCmd(info BuildInfo) *cobra.Command {
	root := &cobra.Command{
		Use:   "fdh",
		Short: "Install Forge harnesses (skills, rules, agents, hooks) across Claude Code, Copilot, Codex, and OpenCode",
		Long:  rootHelpLong,
		Version: fmt.Sprintf("%s (commit %s, built %s)",
			info.Version, info.Commit, info.BuildDate),
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().Bool("json", false, "emit machine-readable JSON instead of the default table output")
	root.PersistentFlags().String("config", "", "path to an explicit config file (default: OS user config dir)")
	root.PersistentFlags().BoolP("verbose", "v", false, "verbose logging to stderr")

	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		return initConfig(cmd)
	}

	root.AddCommand(newInitCmd(info))
	root.AddCommand(newInstallCmd(info))
	root.AddCommand(newUninstallCmd(info))
	root.AddCommand(newListCmd(info))
	root.AddCommand(newListInstalledCmd(info))
	root.AddCommand(newRepairCmd(info))
	root.AddCommand(newScanCmd(info))
	root.AddCommand(newDoctorCmd(info))
	root.AddCommand(newSearchCmd(info))
	root.AddCommand(newConfigCmd(info))
	root.AddCommand(newValidateRegistryCmd(info))
	root.AddCommand(newBundleHashCmd(info))
	root.AddCommand(newUpdateCmd(info))
	root.AddCommand(newSwitchCmd(info))
	root.AddCommand(newInstinctCmd(info))
	root.AddCommand(newEvolveCmd())
	for _, c := range newKindCmds(info) {
		root.AddCommand(c)
	}

	return root
}

const rootHelpLong = `fdh installs Forge harnesses into the AI coding agents
detected on this machine.

A harness is a curated bundle — skills, rules, agents, and hooks — configured
in the Forge platform. You pick one in your project's .fdh/manifest.yaml and
'fdh install' resolves it against the hub and materializes its components.

The catalog lives in a shared Git-backed hub. The installer fans each
component out to every documented path of the target agents, so a single
command makes the harness available across Claude Code, GitHub Copilot,
OpenAI Codex, and OpenCode at once.

Run 'fdh doctor' to see which agents the installer has detected and which
directories it will write to.`

// initConfig wires viper to read configuration from an explicit --config
// path, the per-user config directory, and environment variables prefixed
// with forge_INSTALLER_ (legacy) plus explicit FDH_* bindings for the
// newer keys.
func initConfig(cmd *cobra.Command) error {
	v := viper.GetViper()
	v.SetEnvPrefix("forge_INSTALLER")
	v.AutomaticEnv()

	// Explicit FDH_* env bindings for the HTTP-registry keys. These keys
	// are documented under the FDH_ prefix in the implementation
	// contract; binding here lets them coexist with the legacy
	// forge_INSTALLER_ auto-prefix.
	for env, key := range fdhEnvBindings {
		_ = v.BindEnv(key, env)
	}

	// Sensible defaults — used by config get if nothing else is set.
	v.SetDefault("defaults.scope", "auto")
	v.SetDefault("cache.dir", "")
	v.SetDefault("registry.url", "")
	v.SetDefault("registry.local_path", "")
	v.SetDefault("registry.branch", "main")
	v.SetDefault("registry.kind", "auto")
	v.SetDefault("registry.http.api_version", "v1")
	v.SetDefault("adapters.override", defaultAdaptersOverridePath())

	explicit, _ := cmd.Flags().GetString("config")
	if explicit != "" {
		v.SetConfigFile(explicit)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(defaultConfigDir())
	}
	// A missing config file is not an error — the CLI runs with defaults.
	if err := v.ReadInConfig(); err != nil {
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if !errors.As(err, &configFileNotFoundError) {
			return Errorf(ExitInvalidUsage, "config: %v", err)
		}
		// Fall back to the legacy `forge-installer` config directory
		// for the 90-day deprecation window after the rename. When we read
		// from there, emit a one-line stderr warning suggesting migrate.
		legacy := legacyConfigDir()
		if explicit == "" && legacy != "" {
			if data, err := os.ReadFile(filepath.Join(legacy, "config.yaml")); err == nil {
				v.SetConfigType("yaml")
				if rerr := v.ReadConfig(strings.NewReader(string(data))); rerr == nil {
					fmt.Fprintf(os.Stderr,
						"warning: reading config from legacy path %s — run `fdh config migrate` to move it to the new location.\n",
						filepath.Join(legacy, "config.yaml"))
				}
			}
		}
	}
	return nil
}

// defaultConfigDir returns the per-user config directory under the new
// `fdh` brand name. See `legacyConfigDir` for the deprecated path that
// the CLI falls back to during the 90-day deprecation window.
func defaultConfigDir() string {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cfgDir, "fdh")
}

// legacyConfigDir returns the pre-rename config directory
// (`<user-config>/forge-installer`). Used only as a read-fallback
// during the deprecation window and as the source for `fdh config migrate`.
func legacyConfigDir() string {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cfgDir, "forge-installer")
}

func defaultAdaptersOverridePath() string {
	// Prefer the new path. If the legacy adapters.yaml exists and the new
	// one does not, return the legacy path so existing pilot devs keep
	// working without an explicit migrate run.
	newPath := filepath.Join(defaultConfigDir(), "adapters.yaml")
	if _, err := os.Stat(newPath); err == nil {
		return newPath
	}
	if legacy := legacyConfigDir(); legacy != "" {
		legacyPath := filepath.Join(legacy, "adapters.yaml")
		if _, err := os.Stat(legacyPath); err == nil {
			fmt.Fprintf(os.Stderr,
				"warning: reading adapters override from legacy path %s — run `fdh config migrate`.\n",
				legacyPath)
			return legacyPath
		}
	}
	return newPath
}
