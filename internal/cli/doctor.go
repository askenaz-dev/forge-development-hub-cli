package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/forge/fdh/pkg/adapters"
	"github.com/forge/fdh/pkg/consumerlock"
	"github.com/forge/fdh/pkg/managed"
	"github.com/forge/fdh/pkg/registry"
)

// DoctorReport is the JSON shape emitted by `doctor --json`.
type DoctorReport struct {
	InstallerVersion string                `json:"installer_version"`
	HomeDir          string                `json:"home_dir"`
	ProjectRoot      string                `json:"project_root,omitempty"`
	Registry         RegistryHealth        `json:"registry"`
	Agents           []AgentHealth         `json:"agents"`
	Issues           []DoctorIssue         `json:"issues"`
	ManagedDrift     []ManagedDriftEntry   `json:"managed_drift,omitempty"`
}

// ManagedDriftEntry describes one component path inspected for drift
// against its `.fdh-managed.yaml` marker. Status values:
//
//	ok    - marker exists and content_hash matches recomputed hash
//	user  - no marker; developer-owned directory; informational only
//	drift - marker present but local content hash differs (manual edits)
type ManagedDriftEntry struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Name   string `json:"name,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Agent  string `json:"agent,omitempty"`
}

// AgentHealth describes one agent's detection + path state.
type AgentHealth struct {
	ID        string            `json:"id"`
	Detected  bool              `json:"detected"`
	UserPaths []PathWritability `json:"user_paths"`
	Project   []PathWritability `json:"project_paths,omitempty"`
}

// PathWritability is a single declared path with its writability classification.
type PathWritability struct {
	Path   string `json:"path"`
	State  string `json:"state"` // writable | writable-creatable | unwritable
	Detail string `json:"detail,omitempty"`
}

// RegistryHealth describes the registry's status.
type RegistryHealth struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source"`
	Reachable  bool   `json:"reachable"`
	Detail     string `json:"detail,omitempty"`
	// Kind names the configured transport: "git" | "http" | "local".
	// Optional/additive for backwards compatibility — older consumers
	// that only key off Source/Reachable continue to work unchanged.
	Kind string `json:"kind,omitempty"`
	// Transport is a short human-friendly transport label, e.g. "http v1"
	// or "git". Optional/additive alongside Kind.
	Transport string `json:"transport,omitempty"`
}

// DoctorIssue records a single error/warning in the report.
type DoctorIssue struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

func newDoctorCmd(info BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Detect installed agents, verify writable paths, ping the registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd, args, info)
		},
	}
	return cmd
}

func runDoctor(cmd *cobra.Command, args []string, info BuildInfo) error {
	verbose, _ := cmd.PersistentFlags().GetBool("verbose")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rc, err := buildRunContext(ctx, info, verbose)
	if err != nil {
		return err
	}

	report := DoctorReport{
		InstallerVersion: info.Version,
		HomeDir:          rc.HomeDir,
		ProjectRoot:      rc.ProjectRoot,
	}

	// Registry health.
	if rc.Registry == nil {
		report.Registry = RegistryHealth{
			Configured: false,
			Detail:     "no registry configured (set registry.local_path or registry.url)",
		}
		report.Issues = append(report.Issues, DoctorIssue{Severity: "warning", Message: report.Registry.Detail})
	} else {
		report.Registry.Configured = true
		report.Registry.Source = rc.Registry.Source()
		report.Registry.Kind, report.Registry.Transport = classifyRegistry(rc.Registry)
		if _, err := rc.Registry.Index(rc.Ctx); err != nil {
			report.Registry.Reachable = false
			report.Registry.Detail = err.Error()
			report.Issues = append(report.Issues, DoctorIssue{
				Severity: "error",
				Message:  fmt.Sprintf("registry unreachable: %v", err),
			})
		} else {
			report.Registry.Reachable = true
		}
	}

	// Per-agent detection + writability.
	probeCtx := adapters.ProbeContext{
		HomeDir:     rc.HomeDir,
		ProjectRoot: rc.ProjectRoot,
	}
	detections := rc.Adapters.DetectAll(probeCtx)
	for i, det := range detections {
		agent := rc.Adapters.Agents[i]
		ah := AgentHealth{ID: det.AgentID, Detected: det.Detected}
		// User paths.
		for _, raw := range agent.Paths.User {
			template := stripNamePlaceholder(raw)
			expanded, err := adapters.ExpandPath(template, rc.HomeDir, rc.ProjectRoot, "")
			if err != nil {
				ah.UserPaths = append(ah.UserPaths, PathWritability{Path: raw, State: "unwritable", Detail: err.Error()})
				continue
			}
			rep := adapters.CheckWritable(expanded)
			ah.UserPaths = append(ah.UserPaths, PathWritability{
				Path:   expanded,
				State:  writabilityName(rep.State),
				Detail: rep.Detail,
			})
			if rep.State == adapters.Unwritable && det.Detected {
				report.Issues = append(report.Issues, DoctorIssue{
					Severity: "error",
					Message:  fmt.Sprintf("agent %s: user path %s is unwritable (%s)", agent.ID, expanded, rep.Detail),
				})
			}
		}
		// Project paths — only if a project root is present.
		if rc.ProjectRoot != "" {
			for _, raw := range agent.Paths.Project {
				template := stripNamePlaceholder(raw)
				expanded, err := adapters.ExpandPath(template, rc.HomeDir, rc.ProjectRoot, "")
				if err != nil {
					ah.Project = append(ah.Project, PathWritability{Path: raw, State: "unwritable", Detail: err.Error()})
					continue
				}
				rep := adapters.CheckWritable(expanded)
				ah.Project = append(ah.Project, PathWritability{
					Path:   expanded,
					State:  writabilityName(rep.State),
					Detail: rep.Detail,
				})
				if rep.State == adapters.Unwritable && det.Detected {
					report.Issues = append(report.Issues, DoctorIssue{
						Severity: "error",
						Message:  fmt.Sprintf("agent %s: project path %s is unwritable (%s)", agent.ID, expanded, rep.Detail),
					})
				}
			}
		}
		report.Agents = append(report.Agents, ah)
	}

	// Managed drift: scan known install dirs for markers + drift.
	// TODO(installation-state-ledger): also compare against the state
	// ledger to surface cases (a) "state references missing path" and
	// (b) "marker without state entry" once the ledger lands.
	report.ManagedDrift = computeManagedDrift(rc)

	// Lifecycle: warn when the consumer's lock pins a since-yanked
	// version. Best-effort: requires a wire-protocol registry that
	// exposes Versions[].Status. If the registry isn't configured or
	// the per-component manifest isn't reachable, we skip this check
	// silently (lock parsing failure shows up under managed_drift).
	if rc.ProjectRoot != "" && rc.Registry != nil {
		report.Issues = append(report.Issues, computeLifecycleWarnings(rc.Ctx, rc)...)
	}

	// Emit + exit code.
	if outputMode(cmd) == "json" {
		if err := emitJSON(cmd.OutOrStdout(), report); err != nil {
			return Wrap(ExitGenericFailure, err)
		}
	} else {
		printDoctorTable(cmd.OutOrStdout(), report)
	}

	for _, iss := range report.Issues {
		if iss.Severity == "error" {
			return Errorf(ExitGenericFailure, "doctor reported errors")
		}
	}
	return nil
}

// computeManagedDrift walks the install dirs of every shipped
// adapter (both user and project scope when applicable) and classifies
// each candidate directory or flat-file as ok / user / drift.
func computeManagedDrift(rc *runContext) []ManagedDriftEntry {
	out := []ManagedDriftEntry{}
	scopes := []adapters.Scope{adapters.ScopeUser}
	if rc.ProjectRoot != "" {
		scopes = append(scopes, adapters.ScopeProject)
	}
	for _, a := range adapters.AllSkillAdapters() {
		for _, scope := range scopes {
			root, err := adapterScopeRoot(a, rc.HomeDir, rc.ProjectRoot, scope)
			if err != nil {
				continue
			}
			info, statErr := os.Stat(root)
			if statErr != nil || !info.IsDir() {
				continue
			}
			if a.SupportsSubresources() {
				out = append(out, scanDriftDirectory(a.Agent(), root)...)
			} else {
				out = append(out, scanDriftFlat(a.Agent(), root)...)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func scanDriftDirectory(agent, root string) []ManagedDriftEntry {
	var out []ManagedDriftEntry
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		markerPath := filepath.Join(dir, managed.Filename)
		legacy := filepath.Join(dir, ".skill-version")
		var m managed.Marker
		var hasMarker bool
		if mm, err := managed.Read(markerPath); err == nil {
			m = mm
			hasMarker = true
		} else if mm, err := managed.Read(legacy); err == nil {
			m = mm
			hasMarker = true
		}
		if !hasMarker {
			out = append(out, ManagedDriftEntry{Path: dir, Status: "user", Agent: agent})
			continue
		}
		hash, hErr := adapters.ComputeContentHash(dir)
		status := "ok"
		if hErr == nil && m.ContentHash != "" && hash != m.ContentHash {
			status = "drift"
		}
		out = append(out, ManagedDriftEntry{
			Path:   dir,
			Status: status,
			Name:   m.Name,
			Kind:   valueOr(m.Kind, managed.KindSkill),
			Agent:  agent,
		})
	}
	return out
}

func scanDriftFlat(agent, root string) []ManagedDriftEntry {
	var out []ManagedDriftEntry
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fname := e.Name()
		if !managed.IsAnyMarkerFilename(fname) {
			continue
		}
		markerPath := filepath.Join(root, fname)
		m, err := managed.Read(markerPath)
		if err != nil {
			continue
		}
		out = append(out, ManagedDriftEntry{
			Path:   markerPath,
			Status: "ok", // content hash compare per-file is meaningful but skipped here
			Name:   m.Name,
			Kind:   valueOr(m.Kind, managed.KindSkill),
			Agent:  agent,
		})
	}
	return out
}

// computeLifecycleWarnings inspects .fdh/lock.yaml and queries the
// wire-protocol registry for each entry's manifest, raising an
// `error` issue when a lock entry pins a since-yanked version, and a
// `warning` when a deprecated version is in use.
func computeLifecycleWarnings(ctx context.Context, rc *runContext) []DoctorIssue {
	var out []DoctorIssue
	lock, err := consumerlock.Read(rc.ProjectRoot)
	if err != nil {
		return out
	}
	check := func(kind string, entries []consumerlock.LockEntry) {
		for _, e := range entries {
			// Wire-protocol Manifest call (kind-aware via KindAware
			// when available, falls back to legacy skill-only).
			var m registry.Manifest
			var mErr error
			if ka, ok := rc.Registry.(registry.KindAware); ok {
				m, mErr = ka.ManifestByKind(ctx, kind, "", e.Name)
			} else if kind == "skill" {
				m, mErr = rc.Registry.Manifest(ctx, "", e.Name)
			} else {
				continue
			}
			if mErr != nil {
				continue
			}
			v := m.FindVersion(e.Version)
			if v == nil {
				continue
			}
			if v.IsYanked() {
				out = append(out, DoctorIssue{
					Severity: "error",
					Message:  fmt.Sprintf("lock pins yanked version: %s/%s@%s — re-resolve with `fdh install`", kind, e.Name, e.Version),
				})
			} else if v.IsDeprecated() {
				out = append(out, DoctorIssue{
					Severity: "warning",
					Message:  fmt.Sprintf("lock pins deprecated version: %s/%s@%s", kind, e.Name, e.Version),
				})
			}
		}
	}
	check("skill", lock.Skills)
	check("rule", lock.Rules)
	check("agent", lock.Agents)
	check("hook", lock.Hooks)
	return out
}

func stripNamePlaceholder(raw string) string {
	raw = strings.TrimSuffix(raw, "<name>/")
	raw = strings.TrimSuffix(raw, "<name>")
	if raw == "" {
		raw = "."
	}
	return raw
}

func writabilityName(s adapters.Writability) string {
	switch s {
	case adapters.WritableExisting:
		return "writable"
	case adapters.WritableCreatable:
		return "writable-creatable"
	case adapters.Unwritable:
		return "unwritable"
	}
	return "unknown"
}

func printDoctorTable(w io.Writer, r DoctorReport) {
	fmt.Fprintf(w, "Installer:    %s\n", r.InstallerVersion)
	fmt.Fprintf(w, "Home dir:     %s\n", r.HomeDir)
	if r.ProjectRoot != "" {
		fmt.Fprintf(w, "Project root: %s\n", r.ProjectRoot)
	} else {
		fmt.Fprintln(w, "Project root: (none — user scope only)")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Registry:")
	if !r.Registry.Configured {
		fmt.Fprintf(w, "  not configured (%s)\n", r.Registry.Detail)
	} else {
		state := "unreachable"
		if r.Registry.Reachable {
			state = "reachable"
		}
		if r.Registry.Transport != "" {
			fmt.Fprintf(w, "  transport: %s\n", r.Registry.Transport)
		}
		fmt.Fprintf(w, "  source: %s  [%s]", r.Registry.Source, state)
		if r.Registry.Detail != "" {
			fmt.Fprintf(w, " (%s)", r.Registry.Detail)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Agents:")
	for _, a := range r.Agents {
		mark := "not detected"
		if a.Detected {
			mark = "DETECTED"
		}
		fmt.Fprintf(w, "  %-12s %s\n", a.ID, mark)
		for _, p := range a.UserPaths {
			fmt.Fprintf(w, "    user    %-22s %s\n", p.State, p.Path)
		}
		for _, p := range a.Project {
			fmt.Fprintf(w, "    project %-22s %s\n", p.State, p.Path)
		}
	}
	if len(r.Issues) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Issues:")
		for _, iss := range r.Issues {
			fmt.Fprintf(w, "  [%s] %s\n", iss.Severity, iss.Message)
		}
	}
}

// classifyRegistry inspects the concrete type backing the Registry
// interface and returns (kind, transport). "local" is reserved for
// future use; today every GitRegistry — with or without a RemoteURL —
// reports as "git" so callers can distinguish git from http without
// reaching for an additional viper read.
func classifyRegistry(r registry.Registry) (kind, transport string) {
	switch rr := r.(type) {
	case *registry.HTTPRegistry:
		v := rr.APIVersion
		if v == "" {
			v = "v1"
		}
		return "http", "http " + v
	case *registry.GitRegistry:
		if rr.RemoteURL == "" {
			return "local", "local"
		}
		return "git", "git"
	default:
		return "", ""
	}
}
