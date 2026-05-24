package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"

	"github.com/forge/fdh/pkg/adapters"
	"github.com/forge/fdh/pkg/registry"
)

// runContext bundles the runtime values every subcommand needs.
type runContext struct {
	Ctx          context.Context
	HomeDir      string
	ProjectRoot  string // empty if not inside a project
	Adapters     *adapters.Manifest
	Registry     registry.Registry
	BuildVersion string
	Verbose      bool
}

// buildRunContext constructs a runContext from environment + config.
func buildRunContext(ctxIn context.Context, info BuildInfo, verbose bool) (*runContext, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, Errorf(ExitGenericFailure, "cannot determine home directory: %v", err)
	}
	root := detectProjectRoot()

	overridePath := viper.GetString("adapters.override")
	mani, err := adapters.LoadWithOverride(overridePath)
	if err != nil {
		return nil, Errorf(ExitInvalidUsage, "load adapter map: %v", err)
	}

	reg, err := buildRegistry(verbose)
	if err != nil {
		// Build-time errors are user-visible but not always blocking; some
		// commands (config) work without a registry. Callers decide whether
		// to escalate.
		//nolint:nilerr // intentional: degrade to no-registry context
		return &runContext{
			Ctx: ctxIn, HomeDir: home, ProjectRoot: root, Adapters: mani,
			BuildVersion: info.Version, Verbose: verbose,
		}, nil
	}

	return &runContext{
		Ctx: ctxIn, HomeDir: home, ProjectRoot: root,
		Adapters: mani, Registry: reg, BuildVersion: info.Version, Verbose: verbose,
	}, nil
}

func buildRegistry(verbose bool) (registry.Registry, error) {
	local := viper.GetString("registry.local_path")
	remote := viper.GetString("registry.url")
	if local == "" && remote == "" {
		return nil, errors.New("no registry configured (set registry.local_path or registry.url)")
	}
	if local == "" {
		// Derive a local cache path under the user config dir.
		base := defaultConfigDir()
		if base == "" {
			base = "."
		}
		safe := sanitizePathSegment(remote)
		local = filepath.Join(base, "registry-cache", safe)
	}
	branch := viper.GetString("registry.branch")
	var logger func(string)
	if verbose {
		logger = func(line string) {
			fmt.Fprintln(os.Stderr, "[registry] "+line)
		}
	}
	return &registry.GitRegistry{
		LocalPath: local,
		RemoteURL: remote,
		Branch:    branch,
		Logger:    logger,
	}, nil
}

// detectProjectRoot walks up from CWD looking for the closest directory that
// contains either a .git/ folder or one of the well-known project anchors.
// Returns "" if none found.
func detectProjectRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	cur := cwd
	for {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

func sanitizePathSegment(s string) string {
	// Replace forbidden filename chars with underscores so cache paths are
	// valid on Windows and Unix.
	if s == "" {
		return "default"
	}
	bad := []string{":", "/", "\\", "*", "?", "<", ">", "|", "\""}
	out := s
	for _, b := range bad {
		out = strings.ReplaceAll(out, b, "_")
	}
	return out
}

// resolveScope maps the user's --scope choice into a concrete Scope.
//
//   - "user" or "project" → that scope literally
//   - "auto" or "" → project if a project root was detected, else user
//
// On user scope a ProjectRoot is not required; on project scope a missing
// project root is a fatal usage error.
func resolveScope(scope string, rc *runContext) (adapters.Scope, error) {
	switch strings.ToLower(scope) {
	case "user":
		return adapters.ScopeUser, nil
	case "project":
		if rc.ProjectRoot == "" {
			return "", Errorf(ExitInvalidUsage, "--scope project requires a detectable project root (.git/) at or above %s", currentDir())
		}
		return adapters.ScopeProject, nil
	case "", "auto":
		if rc.ProjectRoot != "" {
			return adapters.ScopeProject, nil
		}
		return adapters.ScopeUser, nil
	default:
		return "", Errorf(ExitInvalidUsage, "unknown --scope %q (expected user|project|auto)", scope)
	}
}

func currentDir() string {
	d, err := os.Getwd()
	if err != nil {
		return "(unknown)"
	}
	return d
}
