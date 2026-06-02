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

// buildRegistry constructs the configured Registry implementation. The
// transport is chosen by `registry.kind` (auto | git | http) with the
// default `auto` applying a URL heuristic:
//
//   - URL ends in .git or starts with git@/ssh://./git:// → GitRegistry
//   - URL starts with https:// or http:// (no .git suffix) → HTTPRegistry
//   - registry.local_path is set → GitRegistry in local-path mode
//
// An explicit `registry.kind` always wins.
func buildRegistry(verbose bool) (registry.Registry, error) {
	local := viper.GetString("registry.local_path")
	remote := viper.GetString("registry.url")
	if local == "" && remote == "" {
		return nil, errors.New("no registry configured (set registry.local_path or registry.url)")
	}

	// local_path always means "GitRegistry pointed at this directory" —
	// it predates registry.kind and keeps existing pilot setups working.
	if local != "" {
		return buildGitRegistry(local, remote, verbose), nil
	}

	kind := strings.ToLower(strings.TrimSpace(viper.GetString("registry.kind")))
	if kind == "" {
		kind = "auto"
	}

	switch kind {
	case "git":
		return buildGitRegistry("", remote, verbose), nil
	case "http":
		return buildHTTPRegistry(remote, verbose)
	case "auto":
		switch {
		case isGitURL(remote):
			return buildGitRegistry("", remote, verbose), nil
		case isHTTPURL(remote):
			return buildHTTPRegistry(remote, verbose)
		default:
			return nil, fmt.Errorf("cannot auto-detect registry transport from %q; set registry.kind to git or http", remote)
		}
	default:
		return nil, fmt.Errorf("unknown registry.kind %q (expected auto|git|http)", kind)
	}
}

// buildGitRegistry assembles a *GitRegistry from viper config. localPath
// may be empty (in which case a cache path is derived from the URL); when
// non-empty it overrides the cache layout (local-path mode).
func buildGitRegistry(localPath, remote string, verbose bool) *registry.GitRegistry {
	if localPath == "" {
		base := defaultConfigDir()
		if base == "" {
			base = "."
		}
		localPath = filepath.Join(base, "registry-cache", sanitizePathSegment(remote))
	}
	branch := viper.GetString("registry.branch")
	return &registry.GitRegistry{
		LocalPath: localPath,
		RemoteURL: remote,
		Branch:    branch,
		Logger:    verboseRegistryLogger(verbose),
	}
}

// buildHTTPRegistry assembles a *HTTPRegistry from viper config.
//
//   - BaseURL is normalized to always end in "/" so URL composition is sane.
//   - CacheDir defaults to <userCacheDir>/fdh/http-cache/ (XDG-friendly).
//   - APIVersion defaults to "v1".
//   - Auth is populated from registry.http.auth.* keys; zero-valued
//     fields mean "no auth on that channel".
func buildHTTPRegistry(remote string, verbose bool) (*registry.HTTPRegistry, error) {
	if remote == "" {
		return nil, errors.New("registry.kind=http but registry.url is empty")
	}
	if !isHTTPURL(remote) {
		return nil, fmt.Errorf("registry.kind=http requires an http(s) URL, got %q", remote)
	}
	base := remote
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}

	apiVersion := viper.GetString("registry.http.api_version")
	if apiVersion == "" {
		apiVersion = "v1"
	}

	cacheDir := viper.GetString("cache.dir")
	if cacheDir == "" {
		root, err := os.UserCacheDir()
		if err != nil || root == "" {
			// Fall back to the config dir if the OS doesn't expose a
			// separate cache dir.
			root = defaultConfigDir()
		}
		if root == "" {
			return nil, errors.New("cannot determine user cache directory for HTTP registry")
		}
		cacheDir = filepath.Join(root, "fdh", "http-cache")
	}

	return &registry.HTTPRegistry{
		BaseURL:    base,
		APIVersion: apiVersion,
		CacheDir:   cacheDir,
		Auth: registry.HTTPAuth{
			Bearer:     viper.GetString("registry.http.auth.bearer"),
			BasicUser:  viper.GetString("registry.http.auth.basic.user"),
			BasicPass:  viper.GetString("registry.http.auth.basic.pass"),
			ClientCert: viper.GetString("registry.http.auth.client_cert"),
			ClientKey:  viper.GetString("registry.http.auth.client_key"),
		},
		Logger: verboseRegistryLogger(verbose),
	}, nil
}

func verboseRegistryLogger(verbose bool) func(string) {
	if !verbose {
		return nil
	}
	return func(line string) {
		fmt.Fprintln(os.Stderr, "[registry] "+line)
	}
}

// isGitURL reports whether u looks like a git remote (suffix .git or one
// of the git/ssh URL schemes).
func isGitURL(u string) bool {
	return strings.HasSuffix(u, ".git") ||
		strings.HasPrefix(u, "git@") ||
		strings.HasPrefix(u, "ssh://") ||
		strings.HasPrefix(u, "git://")
}

// isHTTPURL reports whether u looks like an HTTP(S) URL without a .git
// suffix. The .git check runs first in callers so an https GitHub clone
// URL still routes through Git.
func isHTTPURL(u string) bool {
	return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")
}

// applyLocalScope forces the project root to the current working directory,
// regardless of whether it is a git repo. It backs the `--local` flag:
// devs starting a project in a plain directory want components materialized
// *here* (./.claude/…, ./.agents/…) — and, for init, a ./.fdh/manifest.yaml
// created — rather than installed globally at user scope in their home.
func applyLocalScope(rc *runContext) error {
	cwd, err := os.Getwd()
	if err != nil {
		return Errorf(ExitGenericFailure, "cannot determine current directory: %v", err)
	}
	rc.ProjectRoot = cwd
	return nil
}

// detectProjectRoot walks up from CWD looking for the closest directory that
// is a project anchor: a .git/ folder, or a .fdh/manifest.yaml that a prior
// `fdh init`/`fdh install` materialized. Returns "" if none found.
//
// The .fdh/manifest.yaml anchor (not the bare .fdh/ directory) is deliberate:
// the standalone installer drops a binary at ~/.fdh/bin, so anchoring on the
// directory would make $HOME look like a project for anything run beneath it.
// The manifest file only exists inside an actual fdh-managed project.
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
		if _, err := os.Stat(filepath.Join(cur, ".fdh", "manifest.yaml")); err == nil {
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
