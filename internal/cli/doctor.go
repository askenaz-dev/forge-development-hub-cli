package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/forge/fdh/pkg/adapters"
	"github.com/spf13/cobra"
)

// DoctorReport is the JSON shape emitted by `doctor --json`.
type DoctorReport struct {
	InstallerVersion string                `json:"installer_version"`
	HomeDir          string                `json:"home_dir"`
	ProjectRoot      string                `json:"project_root,omitempty"`
	Registry         RegistryHealth        `json:"registry"`
	Agents           []AgentHealth         `json:"agents"`
	Issues           []DoctorIssue         `json:"issues"`
}

// AgentHealth describes one agent's detection + path state.
type AgentHealth struct {
	ID         string             `json:"id"`
	Detected   bool               `json:"detected"`
	UserPaths  []PathWritability  `json:"user_paths"`
	Project    []PathWritability  `json:"project_paths,omitempty"`
}

// PathWritability is a single declared path with its writability classification.
type PathWritability struct {
	Path  string `json:"path"`
	State string `json:"state"` // writable | writable-creatable | unwritable
	Detail string `json:"detail,omitempty"`
}

// RegistryHealth describes the registry's status.
type RegistryHealth struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source"`
	Reachable  bool   `json:"reachable"`
	Detail     string `json:"detail,omitempty"`
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
