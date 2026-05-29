package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// SupportedConfigKeys is the closed set of keys `config set` accepts.
var SupportedConfigKeys = map[string]string{
	"registry.url":                   "Remote URL of the registry (git remote or HTTP base URL)",
	"registry.local_path":            "Absolute path to a local clone of the registry (forces git transport)",
	"registry.branch":                "Branch of the registry to track (default: main; git transport only)",
	"registry.kind":                  "Registry transport: auto|git|http (default: auto)",
	"registry.http.api_version":      "HTTP registry API version, e.g. v1 (default: v1)",
	"registry.http.auth.bearer":      "Bearer token sent as 'Authorization: Bearer <token>' (HTTP registry)",
	"registry.http.auth.basic.user":  "Basic auth username (HTTP registry)",
	"registry.http.auth.basic.pass":  "Basic auth password (HTTP registry)",
	"registry.http.auth.client_cert": "Absolute path to PEM client certificate for mTLS (HTTP registry)",
	"registry.http.auth.client_key":  "Absolute path to PEM client key for mTLS (HTTP registry)",
	"defaults.scope":                 "Default install scope when none is provided (user|project|auto)",
	"cache.dir":                      "Override the default cache directory (used by HTTP transport)",
	"adapters.override":              "Path to a user-provided adapters.yaml that replaces individual agents",
}

func newConfigCmd(info BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Get or set installer configuration",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "get <key>",
		Short: "Print the current value of a configuration key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigGet(cmd, args)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration key, persisting it to the user config file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigSet(cmd, args)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List all supported configuration keys with their current values",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigList(cmd, args)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "migrate",
		Short: "Move config files from the legacy ~/.config/forge-installer/ to the new ~/.config/fdh/",
		Long: `Migrate the per-user config directory from the deprecated
'forge-installer' name to the new 'fdh' name. This command is idempotent:
running it on an already-migrated machine prints "nothing to migrate".

For 90 days after the rename ships, the CLI also reads from the legacy
directory as a fallback. Running 'fdh config migrate' makes the move
explicit and silences the deprecation warning printed on every load.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigMigrate(cmd, args)
		},
	})
	return cmd
}

func runConfigGet(cmd *cobra.Command, args []string) error {
	key := args[0]
	if _, ok := SupportedConfigKeys[key]; !ok {
		return Errorf(ExitInvalidUsage, "unknown config key %q (run 'config list' for the supported set)", key)
	}
	fmt.Fprintln(cmd.OutOrStdout(), viper.GetString(key))
	return nil
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	key, value := args[0], args[1]
	if _, ok := SupportedConfigKeys[key]; !ok {
		var known []string
		for k := range SupportedConfigKeys {
			known = append(known, k)
		}
		sort.Strings(known)
		return Errorf(ExitInvalidUsage, "unknown config key %q (supported: %v)", key, known)
	}
	viper.Set(key, value)
	if err := writeConfigFile(); err != nil {
		return Wrap(ExitPermission, fmt.Errorf("persist config: %w", err))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "set %s = %s\n", key, value)
	return nil
}

func runConfigList(cmd *cobra.Command, args []string) error {
	if outputMode(cmd) == "json" {
		out := map[string]string{}
		for k := range SupportedConfigKeys {
			out[k] = viper.GetString(k)
		}
		return emitJSON(cmd.OutOrStdout(), out)
	}
	return printConfigList(cmd.OutOrStdout())
}

func printConfigList(w io.Writer) error {
	headers := []string{"KEY", "VALUE", "DESCRIPTION"}
	keys := make([]string, 0, len(SupportedConfigKeys))
	for k := range SupportedConfigKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([][]string, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, []string{k, viper.GetString(k), SupportedConfigKeys[k]})
	}
	return printTable(w, headers, rows)
}

// writeConfigFile persists the current viper state to <config-dir>/config.yaml.
func writeConfigFile() error {
	dir := defaultConfigDir()
	if dir == "" {
		return fmt.Errorf("no user config directory available")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "config.yaml")

	// Only persist the supported, known keys to avoid leaking unrelated viper state.
	out := map[string]any{}
	for k := range SupportedConfigKeys {
		if v := viper.Get(k); v != nil && v != "" {
			setNested(out, k, v)
		}
	}
	buf, err := yaml.Marshal(out)
	if err != nil {
		return err
	}
	return os.WriteFile(path, buf, 0o644)
}

// setNested writes "a.b.c" = v into a nested map structure.
func setNested(m map[string]any, dottedKey string, v any) {
	parts := splitDotted(dottedKey)
	for i := 0; i < len(parts)-1; i++ {
		next, ok := m[parts[i]].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[parts[i]] = next
		}
		m = next
	}
	m[parts[len(parts)-1]] = v
}

// runConfigMigrate moves config files from the legacy
// '<user-config>/forge-installer/' directory to '<user-config>/fdh/'.
//
// Idempotent: if the legacy directory is missing or the new files already
// exist, the command reports "nothing to migrate" and exits zero.
func runConfigMigrate(cmd *cobra.Command, args []string) error {
	legacy := legacyConfigDir()
	current := defaultConfigDir()
	if legacy == "" || current == "" {
		return Errorf(ExitGenericFailure, "config: cannot resolve user config directory")
	}

	// Files we know to migrate. If new files appear in supported config in
	// the future, list them here.
	candidates := []string{"config.yaml", "adapters.yaml"}

	moved := []string{}
	skipped := []string{}

	for _, name := range candidates {
		src := filepath.Join(legacy, name)
		dst := filepath.Join(current, name)
		// Skip if legacy file absent.
		if _, err := os.Stat(src); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return Wrap(ExitGenericFailure, fmt.Errorf("stat %s: %w", src, err))
		}
		// Skip if destination already exists — never clobber the new file.
		if _, err := os.Stat(dst); err == nil {
			skipped = append(skipped, name+" (already present at new path)")
			continue
		}
		// Ensure destination dir exists.
		if err := os.MkdirAll(current, 0o755); err != nil {
			return Wrap(ExitPermission, fmt.Errorf("mkdir %s: %w", current, err))
		}
		// Copy then remove (rename across drives can fail on Windows).
		data, err := os.ReadFile(src)
		if err != nil {
			return Wrap(ExitGenericFailure, fmt.Errorf("read %s: %w", src, err))
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return Wrap(ExitPermission, fmt.Errorf("write %s: %w", dst, err))
		}
		if err := os.Remove(src); err != nil {
			// Non-fatal: the migration succeeded; the legacy file lingers.
			fmt.Fprintf(cmd.ErrOrStderr(),
				"warning: copied %s but could not remove legacy source: %v\n", name, err)
		}
		moved = append(moved, name)
	}

	out := cmd.OutOrStdout()
	if len(moved) == 0 && len(skipped) == 0 {
		fmt.Fprintln(out, "nothing to migrate (legacy config not found)")
		return nil
	}
	if len(moved) > 0 {
		fmt.Fprintf(out, "Migrated %d file(s) from %s to %s:\n", len(moved), legacy, current)
		for _, m := range moved {
			fmt.Fprintf(out, "  - %s\n", m)
		}
	}
	if len(skipped) > 0 {
		fmt.Fprintf(out, "Skipped %d file(s):\n", len(skipped))
		for _, s := range skipped {
			fmt.Fprintf(out, "  - %s\n", s)
		}
	}
	return nil
}

func splitDotted(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
