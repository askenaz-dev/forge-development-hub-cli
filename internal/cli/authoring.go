package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/forge/fdh/pkg/adapters"
	"github.com/forge/fdh/pkg/bundle"
	"github.com/forge/fdh/pkg/portability"
)

// Authoring verbs (capability fdh-component-authoring): new / sync / share.
// Implemented skills-first — materialization for rule/agent/hook depends on the
// per-kind primitives (hub-rules/agents/hooks-primitive) and is gated until then.

func entrypointFilename(kind string) string {
	switch kind {
	case "skill":
		return "SKILL.md"
	case "rule":
		return "RULE.md"
	case "agent":
		return "AGENT.md"
	case "hook":
		return "HOOK.md"
	}
	return ""
}

// materializeSupported reports whether local materialization (new/sync) is
// implemented for the kind. Only skills today.
func materializeSupported(kind string) bool { return kind == "skill" }

// authoringDir resolves the canonical source directory for a component.
func authoringDir(cmd *cobra.Command, rc *runContext, name string) string {
	if dir, _ := cmd.Flags().GetString("dir"); dir != "" {
		return dir
	}
	root := rc.ProjectRoot
	if root == "" {
		root = "."
	}
	return filepath.Join(root, ".fdh", "authoring", name)
}

// resolveAuthoringTargets returns the agent ids to materialize into: the
// requested ones, or every detected agent, filtered to the adapter map's known
// ids.
func resolveAuthoringTargets(requested []string, rc *runContext) ([]string, error) {
	cand := requested
	if len(cand) == 0 {
		cand = detectedAgentIDs(rc)
	}
	if len(cand) == 0 {
		return nil, Errorf(ExitNoAgent, "no agents requested and none detected; pass --agent (e.g. --agent claude-code)")
	}
	known := map[string]struct{}{}
	for _, id := range rc.Adapters.AgentIDs() {
		known[id] = struct{}{}
	}
	var out []string
	for _, id := range cand {
		if _, ok := known[id]; !ok {
			return nil, Errorf(ExitInvalidUsage, "unknown agent id %q (known: %s)", id, strings.Join(rc.Adapters.AgentIDs(), ","))
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// scaffoldBundle writes a fresh canonical source bundle at dir.
func scaffoldBundle(dir, kind, name, desc string) error {
	for _, sub := range []string{"references", "scripts"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return err
		}
		// .gitkeep keeps the empty dir in the author's git; HashDir and the
		// materializer both skip dotfiles, so it never affects content.
		if err := os.WriteFile(filepath.Join(dir, sub, ".gitkeep"), nil, 0o644); err != nil {
			return err
		}
	}
	ep := entrypointFilename(kind)
	content := fmt.Sprintf("---\nname: %s\nversion: 0.1.0\ndescription: %s\n---\n\n# %s\n\n<!-- Author your %s here. Update the description in the frontmatter above. -->\n",
		name, desc, name, kind)
	return os.WriteFile(filepath.Join(dir, ep), []byte(content), 0o644)
}

// copyTree copies srcDir into destDir, skipping dotfiles/dirs. No breadcrumb,
// no .fdh-managed.yaml marker — authored components are the developer's own and
// unmanaged, so fdh install/update never clobbers them.
func copyTree(srcDir, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	return filepath.Walk(srcDir, func(p string, fi os.FileInfo, err error) error {
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
		if strings.HasPrefix(filepath.Base(p), ".") {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dest := filepath.Join(destDir, rel)
		if fi.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
}

func runKindNew(cmd *cobra.Command, args []string, info BuildInfo, kind string) error {
	name := args[0]
	if !materializeSupported(kind) {
		return Errorf(ExitInvalidUsage,
			"`fdh %s new` is not supported yet — only `skill` is implemented; rule/agent/hook materialization is tracked in hub-%s-primitive", kind, kind)
	}

	verbose, _ := cmd.PersistentFlags().GetBool("verbose")
	rc, err := buildRunContext(cmd.Context(), info, verbose)
	if err != nil {
		return err
	}

	dir := authoringDir(cmd, rc, name)
	ep := entrypointFilename(kind)
	if _, err := os.Stat(filepath.Join(dir, ep)); err == nil {
		return Errorf(ExitInvalidUsage, "a component already exists at %s (delete it or choose another name/--dir)", dir)
	}

	desc, _ := cmd.Flags().GetString("description")
	if strings.TrimSpace(desc) == "" {
		desc = fmt.Sprintf("TODO: describe what %q does and when to use it.", name)
	}

	// Resolve target agents and scope BEFORE writing anything, so a usage error
	// leaves no partial scaffold behind.
	requested, _ := cmd.Flags().GetStringSlice("agent")
	targets, err := resolveAuthoringTargets(requested, rc)
	if err != nil {
		return err
	}
	scopeStr, _ := cmd.Flags().GetString("scope")
	scope, err := resolveScope(scopeStr, rc)
	if err != nil {
		return err
	}

	// Scaffold the canonical source and validate it.
	if err := scaffoldBundle(dir, kind, name, desc); err != nil {
		return Wrap(ExitGenericFailure, fmt.Errorf("scaffold: %w", err))
	}
	b, err := bundle.Load(dir)
	if err != nil {
		return Wrap(ExitGenericFailure, fmt.Errorf("load scaffolded bundle: %w", err))
	}
	if err := b.Validate(); err != nil {
		return Wrap(ExitGenericFailure, fmt.Errorf("validate scaffolded bundle: %w", err))
	}

	// Materialize into each selected agent's directory (unmanaged).
	paths, err := rc.Adapters.PathSet(adapters.PathSetOptions{
		SkillName:   name,
		ProjectRoot: rc.ProjectRoot,
		HomeDir:     rc.HomeDir,
		Scope:       scope,
		AgentIDs:    targets,
	})
	if err != nil {
		return Wrap(ExitGenericFailure, err)
	}
	materialized := make([]string, 0, len(paths))
	for _, p := range paths {
		if err := copyTree(dir, p.Path); err != nil {
			return Wrap(ExitGenericFailure, fmt.Errorf("materialize to %s: %w", p.Path, err))
		}
		materialized = append(materialized, p.Path)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Created %s '%s' (version 0.1.0)\n", kind, name)
	fmt.Fprintf(out, "  canonical source: %s\n", dir)
	fmt.Fprintf(out, "  agents:           %s\n", strings.Join(targets, ", "))
	for _, m := range materialized {
		fmt.Fprintf(out, "  materialized:     %s\n", m)
	}
	fmt.Fprintf(out, "\nNext: edit %s, then `fdh %s sync %s` to propagate, or `fdh %s share %s` to contribute.\n",
		filepath.Join(dir, ep), kind, name, kind, name)
	return nil
}

// materializedDrift reports whether the materialized copy at destDir diverges
// from the canonical source. A missing or unreadable copy counts as drift
// (it needs regenerating). Uses the canonical content hash so dotfiles and
// mtimes are ignored — only real content matters.
func materializedDrift(srcDir, destDir string) bool {
	if _, err := os.Stat(destDir); err != nil {
		return true
	}
	sh, err := bundle.HashDir(srcDir)
	if err != nil {
		return false
	}
	dh, err := bundle.HashDir(destDir)
	if err != nil {
		return true
	}
	return sh != dh
}

func runKindSync(cmd *cobra.Command, args []string, info BuildInfo, kind string) error {
	name := args[0]
	if !materializeSupported(kind) {
		return Errorf(ExitInvalidUsage,
			"`fdh %s sync` is not supported yet — only `skill` is implemented; rule/agent/hook materialization is tracked in hub-%s-primitive", kind, kind)
	}

	verbose, _ := cmd.PersistentFlags().GetBool("verbose")
	rc, err := buildRunContext(cmd.Context(), info, verbose)
	if err != nil {
		return err
	}

	dir := authoringDir(cmd, rc, name)
	ep := entrypointFilename(kind)
	if _, err := os.Stat(filepath.Join(dir, ep)); err != nil {
		return Errorf(ExitInvalidUsage, "no canonical source at %s — run `fdh %s new %s` first", dir, kind, name)
	}
	if b, err := bundle.Load(dir); err != nil {
		return Wrap(ExitGenericFailure, fmt.Errorf("load canonical source: %w", err))
	} else if err := b.Validate(); err != nil {
		return Wrap(ExitGenericFailure, fmt.Errorf("validate canonical source: %w", err))
	}

	requested, _ := cmd.Flags().GetStringSlice("agent")
	targets, err := resolveAuthoringTargets(requested, rc)
	if err != nil {
		return err
	}
	scopeStr, _ := cmd.Flags().GetString("scope")
	scope, err := resolveScope(scopeStr, rc)
	if err != nil {
		return err
	}
	paths, err := rc.Adapters.PathSet(adapters.PathSetOptions{
		SkillName: name, ProjectRoot: rc.ProjectRoot, HomeDir: rc.HomeDir, Scope: scope, AgentIDs: targets,
	})
	if err != nil {
		return Wrap(ExitGenericFailure, err)
	}

	check, _ := cmd.Flags().GetBool("check")
	force, _ := cmd.Flags().GetBool("force")
	out := cmd.OutOrStdout()

	anyDrift := false
	for _, p := range paths {
		drift := materializedDrift(dir, p.Path)
		if check {
			if drift {
				anyDrift = true
				fmt.Fprintf(out, "drift: %s\n", p.Path)
			} else {
				fmt.Fprintf(out, "in sync: %s\n", p.Path)
			}
			continue
		}
		if drift && !force {
			fmt.Fprintf(out, "warning: overwriting locally-edited copy at %s\n", p.Path)
		}
		if err := copyTree(dir, p.Path); err != nil {
			return Wrap(ExitGenericFailure, fmt.Errorf("sync to %s: %w", p.Path, err))
		}
		fmt.Fprintf(out, "synced: %s\n", p.Path)
	}

	if check && anyDrift {
		return Errorf(ExitGenericFailure, "drift detected (run `fdh %s sync %s` to regenerate)", kind, name)
	}
	return nil
}

func kindPlural(kind string) string {
	switch kind {
	case "skill":
		return "skills"
	case "rule":
		return "rules"
	case "agent":
		return "agents"
	case "hook":
		return "hooks"
	}
	return kind + "s"
}

func gitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// registryEntryYAML renders a v2 component entry for hub/registry.yaml. It is
// appended at EOF (components is the last top-level key), preserving the file's
// comments. Always default:false — a merged contribution is not auto-adopted.
func registryEntryYAML(kind, name, desc, ownerTeam string, agents []string) string {
	if ownerTeam == "" {
		ownerTeam = "unassigned"
	}
	if len(agents) == 0 {
		if kind == "skill" {
			agents = []string{"claude-code", "codex", "copilot", "opencode"}
		} else {
			agents = []string{"claude-code"}
		}
	}
	descEsc := strings.ReplaceAll(desc, `"`, `'`)
	var b strings.Builder
	fmt.Fprintf(&b, "\n  - name: %s\n", name)
	fmt.Fprintf(&b, "    kind: %s\n", kind)
	fmt.Fprintf(&b, "    description: \"%s\"\n", descEsc)
	fmt.Fprintf(&b, "    owner_team: %s\n", ownerTeam)
	fmt.Fprintf(&b, "    tags: []\n")
	fmt.Fprintf(&b, "    default: false\n")
	fmt.Fprintf(&b, "    min_fdh_version: \"0.4.0\"\n")
	fmt.Fprintf(&b, "    agents_supported: [%s]\n", strings.Join(agents, ", "))
	fmt.Fprintf(&b, "    path: %s/%s\n", kindPlural(kind), name)
	return b.String()
}

func runKindShare(cmd *cobra.Command, args []string, info BuildInfo, kind string) error {
	name := args[0]
	verbose, _ := cmd.PersistentFlags().GetBool("verbose")
	rc, err := buildRunContext(cmd.Context(), info, verbose)
	if err != nil {
		return err
	}

	// 1. Locate + validate the canonical source (abort before touching the hub).
	dir := authoringDir(cmd, rc, name)
	if _, err := os.Stat(filepath.Join(dir, entrypointFilename(kind))); err != nil {
		return Errorf(ExitInvalidUsage, "no canonical source at %s — run `fdh %s new %s` first", dir, kind, name)
	}
	b, err := bundle.Load(dir)
	if err != nil {
		return Wrap(ExitGenericFailure, fmt.Errorf("load source: %w", err))
	}
	if err := b.Validate(); err != nil {
		return Wrap(ExitGenericFailure, fmt.Errorf("validate source: %w", err))
	}
	if findings := portability.Lint(b, portability.LintOptions{KnownAgentIDs: rc.Adapters.AgentIDs()}); portability.HasErrors(findings) {
		fmt.Fprintln(cmd.ErrOrStderr(), portability.Format(findings))
		return Errorf(ExitPortability, "portability lint failed — fix before sharing")
	}

	// 2. Acquire the hub checkout.
	repo, _ := cmd.Flags().GetString("repo")
	if repo == "" {
		return Errorf(ExitInvalidUsage, "pass --repo <path-to-forge-development-hub checkout> (auto-clone from registry config is not wired yet)")
	}
	if _, err := os.Stat(filepath.Join(repo, "hub", "registry.yaml")); err != nil {
		return Errorf(ExitInvalidUsage, "%s does not look like a hub checkout (missing hub/registry.yaml)", repo)
	}
	dest := filepath.Join(repo, kindPlural(kind), name)
	if _, err := os.Stat(dest); err == nil {
		return Errorf(ExitInvalidUsage, "%s already exists in the hub — choose another name or update it in a separate change", filepath.Join(kindPlural(kind), name))
	}

	// 3. Branch off the base, clean.
	base, _ := cmd.Flags().GetString("base")
	branch := fmt.Sprintf("share/%s/%s", kind, name)
	if out, err := gitIn(repo, "checkout", "-q", base); err != nil {
		return Wrap(ExitGenericFailure, fmt.Errorf("git checkout %s: %w: %s", base, err, out))
	}
	if out, err := gitIn(repo, "checkout", "-q", "-b", branch); err != nil {
		return Wrap(ExitGenericFailure, fmt.Errorf("git checkout -b %s: %w: %s", branch, err, out))
	}

	// 4. Copy the bundle + append the registry entry (default:false).
	if err := copyTree(dir, dest); err != nil {
		return Wrap(ExitGenericFailure, fmt.Errorf("copy bundle into hub: %w", err))
	}
	agents, _ := cmd.Flags().GetStringSlice("agent")
	ownerTeam, _ := cmd.Flags().GetString("owner-team")
	regPath := filepath.Join(repo, "hub", "registry.yaml")
	regData, err := os.ReadFile(regPath)
	if err != nil {
		return Wrap(ExitGenericFailure, err)
	}
	entry := registryEntryYAML(kind, name, b.SkillMD.Description, ownerTeam, agents)
	if err := os.WriteFile(regPath, append(regData, []byte(entry)...), 0o644); err != nil {
		return Wrap(ExitGenericFailure, err)
	}

	// 5. Commit with the CLI-authored conventional scope.
	if out, err := gitIn(repo, "add", "-A"); err != nil {
		return Wrap(ExitGenericFailure, fmt.Errorf("git add: %w: %s", err, out))
	}
	msg := fmt.Sprintf("feat(%s): add %s", name, kind)
	if out, err := gitIn(repo, "commit", "-q", "-m", msg); err != nil {
		return Wrap(ExitGenericFailure, fmt.Errorf("git commit: %w: %s", err, out))
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Prepared contribution on branch %s:\n", branch)
	fmt.Fprintf(out, "  + %s/\n", filepath.Join(kindPlural(kind), name))
	fmt.Fprintf(out, "  + hub/registry.yaml entry (default: false)\n")
	fmt.Fprintf(out, "  commit: %s\n", msg)

	// 6. Push + open PR (unless dry-run). Never merges.
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if dryRun {
		fmt.Fprintf(out, "\n--dry-run: branch + commit prepared locally; not pushed. Push and open a PR when ready.\n")
		return nil
	}
	if pushOut, err := gitIn(repo, "push", "-u", "origin", branch); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "git push to origin failed (%v): %s\n", err, pushOut)
		fmt.Fprintf(cmd.ErrOrStderr(), "If you lack write access, fork the hub and push there: `gh repo fork --remote` then re-run with that remote.\n")
		return Wrap(ExitGenericFailure, fmt.Errorf("push failed"))
	}
	if _, err := exec.LookPath("gh"); err != nil {
		fmt.Fprintf(out, "\nBranch pushed. Install `gh` (or open the PR manually) to finish.\n")
		return nil //nolint:nilerr // gh absence is a graceful exit, not an error
	}
	prCmd := exec.Command("gh", "pr", "create", "--fill", "--head", branch, "--base", base)
	prCmd.Dir = repo
	prOut, err := prCmd.CombinedOutput()
	if err != nil {
		return Wrap(ExitGenericFailure, fmt.Errorf("gh pr create: %w: %s", err, strings.TrimSpace(string(prOut))))
	}
	fmt.Fprintf(out, "\n%s\n", strings.TrimSpace(string(prOut)))
	fmt.Fprintf(out, "PR opened. It is NOT part of the hub until a reviewer approves and a publisher merges it.\n")
	return nil
}
