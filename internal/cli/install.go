package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/forge/fdh/pkg/adapters"
	"github.com/forge/fdh/pkg/bundle"
	"github.com/forge/fdh/pkg/portability"
	"github.com/forge/fdh/pkg/provenance"
	"github.com/forge/fdh/pkg/registry"
)

// InstallResult is the JSON shape emitted by `install --json`.
type InstallResult struct {
	Skill        string             `json:"skill"`
	Namespace    string             `json:"namespace"`
	Name         string             `json:"name"`
	Version      string             `json:"version"`
	ContentHash  string             `json:"content_hash"`
	Scope        string             `json:"scope"`
	Registry     string             `json:"registry"`
	TargetAgents []string           `json:"target_agents"`
	Writes       []InstallWriteInfo `json:"writes"`
}

// InstallWriteInfo describes one written destination path.
type InstallWriteInfo struct {
	Path   string   `json:"path"`
	Agents []string `json:"agents"`
}

func newInstallCmd(info BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install <skill>[@version]",
		Short: "Install a skill from the registry",
		Long:  installHelp,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(cmd, args, info)
		},
	}
	cmd.Flags().StringSlice("agent", nil, "agent id to target (may be repeated). Default: every detected agent.")
	cmd.Flags().String("scope", "auto", "install scope: user|project|auto")
	return cmd
}

const installHelp = `Install a skill from the configured registry.

By default, the bundle is installed to every detected agent at the
appropriate scope (project if a project root is detectable, otherwise
user). Use --agent <id> (repeatable) to target a specific agent or
--scope user|project to override the default.

A skill reference is "<namespace>/<name>" optionally followed by
"@<version>"; omitting the version installs the latest published version
recorded in the registry index.`

func runInstall(cmd *cobra.Command, args []string, info BuildInfo) error {
	verbose, _ := cmd.PersistentFlags().GetBool("verbose")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	rc, err := buildRunContext(ctx, info, verbose)
	if err != nil {
		return err
	}
	if rc.Registry == nil {
		return Errorf(ExitInvalidUsage, "no registry configured. Run 'fdh config set registry.local_path /path' or 'registry.url <git-url>' first.")
	}

	ref := args[0]
	namespace, name, version, err := parseSkillRef(ref)
	if err != nil {
		return Errorf(ExitInvalidUsage, "invalid skill reference: %v", err)
	}

	scopeStr, _ := cmd.Flags().GetString("scope")
	scope, err := resolveScope(scopeStr, rc)
	if err != nil {
		return err
	}

	// Resolve registry data.
	manifest, err := rc.Registry.Manifest(rc.Ctx, namespace, name)
	if err != nil {
		var unreach registry.RegistryUnreachable
		if errors.As(err, &unreach) {
			return Wrap(ExitRegistryUnreach, err)
		}
		return Wrap(ExitGenericFailure, fmt.Errorf("read manifest %s/%s: %w", namespace, name, err))
	}
	if version == "" {
		version = manifest.Latest
	}
	if manifest.FindVersion(version) == nil {
		return Errorf(ExitInvalidUsage, "version %s not found in manifest for %s/%s", version, namespace, name)
	}

	// Fetch and hash-verify the bundle.
	bp, err := rc.Registry.FetchBundle(rc.Ctx, namespace, name, version)
	if err != nil {
		var unreach registry.RegistryUnreachable
		if errors.As(err, &unreach) {
			return Wrap(ExitRegistryUnreach, err)
		}
		return Wrap(ExitGenericFailure, err)
	}
	defer bp.Cleanup()

	// Load the bundle, validate, lint.
	b, err := bundle.Load(bp.Path)
	if err != nil {
		return Wrap(ExitGenericFailure, fmt.Errorf("load bundle: %w", err))
	}
	if err := b.Validate(); err != nil {
		return Wrap(ExitGenericFailure, err)
	}
	known := rc.Adapters.AgentIDs()
	lint := portability.Lint(b, portability.LintOptions{KnownAgentIDs: known})
	if portability.HasErrors(lint) {
		fmt.Fprintln(cmd.ErrOrStderr(), portability.Format(lint))
		return Errorf(ExitPortability, "portability lint failed")
	}

	// Determine target agents.
	requested, _ := cmd.Flags().GetStringSlice("agent")
	targets, err := resolveTargets(requested, rc, b.SkillMD)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return Errorf(ExitNoAgent, "no compatible target agents (none detected on host or none in compatibility list)")
	}

	// Compute path-set union.
	pathOpts := adapters.PathSetOptions{
		SkillName:   b.SkillMD.Name,
		ProjectRoot: rc.ProjectRoot,
		HomeDir:     rc.HomeDir,
		Scope:       scope,
		AgentIDs:    targets,
	}
	paths, err := rc.Adapters.PathSet(pathOpts)
	if err != nil {
		return Wrap(ExitGenericFailure, err)
	}

	// Build the breadcrumb ref.
	registryDisplay := rc.Registry.Source()
	breadcrumb := provenance.MakeBreadcrumbRef(registryDisplay, namespace, name, version)

	// Pre-flight Windows long-path check.
	for _, p := range paths {
		if err := CheckLongPath(p.Path); err != nil {
			return Wrap(ExitGenericFailure, err)
		}
	}

	// Fan-out write.
	writes := make([]InstallWriteInfo, 0, len(paths))
	for _, p := range paths {
		if err := writeBundleToPath(bp.Path, p.Path, breadcrumb); err != nil {
			if errors.Is(err, os.ErrPermission) {
				return Wrap(ExitPermission, fmt.Errorf("write to %s: %w", p.Path, err))
			}
			return Wrap(ExitGenericFailure, fmt.Errorf("write to %s: %w", p.Path, err))
		}
		// Write sidecar.
		meta := provenance.SkillMeta{
			Registry:         registryDisplay,
			Namespace:        namespace,
			Name:             name,
			Version:          version,
			ContentHash:      bp.Hash,
			InstalledBy:      installerActor(),
			TargetAgents:     append([]string(nil), p.Agents...),
			Scope:            string(scope),
			Path:             p.Path,
			InstallerVersion: info.Version,
		}
		if err := provenance.WriteSidecar(p.Path, meta); err != nil {
			return Wrap(ExitGenericFailure, fmt.Errorf("write sidecar: %w", err))
		}
		writes = append(writes, InstallWriteInfo{Path: p.Path, Agents: p.Agents})
	}

	result := InstallResult{
		Skill:        fmt.Sprintf("%s/%s", namespace, name),
		Namespace:    namespace,
		Name:         name,
		Version:      version,
		ContentHash:  bp.Hash,
		Scope:        string(scope),
		Registry:     registryDisplay,
		TargetAgents: targets,
		Writes:       writes,
	}

	if outputMode(cmd) == "json" {
		return emitJSON(cmd.OutOrStdout(), result)
	}
	return printInstallTable(cmd.OutOrStdout(), result)
}

func parseSkillRef(ref string) (namespace, name, version string, err error) {
	at := strings.LastIndex(ref, "@")
	if at >= 0 {
		version = ref[at+1:]
		ref = ref[:at]
		if version == "" {
			return "", "", "", fmt.Errorf("empty version after @")
		}
	}
	slash := strings.Index(ref, "/")
	if slash <= 0 || slash == len(ref)-1 {
		return "", "", "", fmt.Errorf("expected <namespace>/<name>[@<version>], got %q", ref)
	}
	return ref[:slash], ref[slash+1:], version, nil
}

// resolveTargets computes the final agent list given the user's --agent flags,
// the detected agents on the host, and the skill's compatibility allowlist.
func resolveTargets(requested []string, rc *runContext, doc bundle.SkillMDDoc) ([]string, error) {
	detected := detectedAgentIDs(rc)

	candidate := requested
	if len(candidate) == 0 {
		candidate = detected
	}
	if len(candidate) == 0 {
		return nil, Errorf(ExitNoAgent, "no agents requested and none detected; run 'fdh doctor'")
	}

	// Filter by the manifest's known agents.
	known := map[string]struct{}{}
	for _, id := range rc.Adapters.AgentIDs() {
		known[id] = struct{}{}
	}
	var filtered []string
	for _, id := range candidate {
		if _, ok := known[id]; !ok {
			return nil, Errorf(ExitInvalidUsage, "unknown agent id %q (known: %s)", id, strings.Join(rc.Adapters.AgentIDs(), ","))
		}
		filtered = append(filtered, id)
	}

	// Apply compatibility filter for non-portable skills.
	if !doc.IsPortable() {
		allow := map[string]struct{}{}
		for _, c := range doc.Compatibility {
			allow[c] = struct{}{}
		}
		var compatible []string
		for _, id := range filtered {
			if _, ok := allow[id]; ok {
				compatible = append(compatible, id)
			}
		}
		filtered = compatible
	}

	sort.Strings(filtered)
	return filtered, nil
}

// detectedAgentIDs runs the manifest's probes and returns the IDs of agents
// whose probes succeeded.
func detectedAgentIDs(rc *runContext) []string {
	ctx := adapters.ProbeContext{
		HomeDir:     rc.HomeDir,
		ProjectRoot: rc.ProjectRoot,
	}
	results := rc.Adapters.DetectAll(ctx)
	var out []string
	for _, r := range results {
		if r.Detected {
			out = append(out, r.AgentID)
		}
	}
	return out
}

// writeBundleToPath copies the bundle at srcDir into destDir, creating the
// destination directory if necessary, replacing existing files. The
// SKILL.md file is rewritten with the breadcrumb injected.
func writeBundleToPath(srcDir, destDir, breadcrumb string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	return filepath.Walk(srcDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// Skip any hidden sidecar that might have ended up in srcDir.
		if strings.HasPrefix(filepath.Base(p), ".") {
			return nil
		}
		dest := filepath.Join(destDir, rel)
		if info.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		// SKILL.md → inject breadcrumb. Everything else → byte copy.
		if rel == "SKILL.md" {
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out := provenance.InjectBreadcrumb(raw, breadcrumb)
			return os.WriteFile(dest, out, 0o644)
		}
		return copyFile(p, dest, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	// Preserve exec bit on POSIX; Windows is a no-op.
	if mode&0o111 != 0 {
		_ = os.Chmod(dst, 0o755)
	}
	return nil
}

func installerActor() string {
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME") // Windows
	}
	if user == "" {
		user = "unknown"
	}
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return user + "@" + host
}

func printInstallTable(w io.Writer, r InstallResult) error {
	fmt.Fprintf(w, "Installed %s@%s\n", r.Skill, r.Version)
	fmt.Fprintf(w, "  scope:    %s\n", r.Scope)
	fmt.Fprintf(w, "  registry: %s\n", r.Registry)
	fmt.Fprintf(w, "  hash:     %s\n", r.ContentHash)
	fmt.Fprintf(w, "  agents:   %s\n", strings.Join(r.TargetAgents, ","))
	fmt.Fprintln(w, "  wrote:")
	for _, wri := range r.Writes {
		fmt.Fprintf(w, "    - %s  (serves: %s)\n", wri.Path, strings.Join(wri.Agents, ","))
	}
	return nil
}
