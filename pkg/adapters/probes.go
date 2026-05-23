package adapters

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Probe types supported by the loader. Adding a new probe type is a
// localized change: extend this list and the switch in EvaluateProbe.
const (
	ProbeDirExists     = "dir-exists"
	ProbeExecOnPath    = "exec-on-path"
	ProbeShellExitZero = "shell-exit-zero"
)

func validateProbe(p Probe) error {
	switch p.Type {
	case ProbeDirExists:
		if p.Path == "" {
			return fmt.Errorf("dir-exists probe requires path")
		}
	case ProbeExecOnPath:
		if p.Name == "" {
			return fmt.Errorf("exec-on-path probe requires name")
		}
	case ProbeShellExitZero:
		if p.Cmd == "" {
			return fmt.Errorf("shell-exit-zero probe requires cmd")
		}
	default:
		return fmt.Errorf("unknown probe type: %q", p.Type)
	}
	return nil
}

// ProbeContext provides the runtime state probes can use (home dir, current
// project root, etc.). Tests inject a fake context; production wires the real
// values from the cobra command.
type ProbeContext struct {
	HomeDir     string
	ProjectRoot string
	// LookPath shadows exec.LookPath in tests so probes can be evaluated
	// against a synthetic PATH. nil means use the real exec.LookPath.
	LookPath func(string) (string, error)
	// StatDir shadows os.Stat in tests for dir-exists probes. nil means
	// use the real os.Stat.
	StatDir func(string) (os.FileInfo, error)
	// RunShell shadows exec.Command/Run for shell-exit-zero probes.
	// Return nil error on exit 0. nil means use the real shell.
	RunShell func(cmd string) error
}

// EvaluateProbe runs a single probe and returns true on success. Failure to
// resolve a path or look up an executable returns false WITHOUT erroring —
// the goal of a probe is to be safe to evaluate from any environment.
func EvaluateProbe(p Probe, ctx ProbeContext) bool {
	switch p.Type {
	case ProbeDirExists:
		path := p.Path
		// dir-exists may use "~" without a skill name placeholder.
		if strings.HasPrefix(path, "~/") || path == "~" {
			if ctx.HomeDir == "" {
				return false
			}
			path = filepath.Join(ctx.HomeDir, strings.TrimPrefix(path, "~"))
		} else if !filepath.IsAbs(path) && ctx.ProjectRoot != "" {
			path = filepath.Join(ctx.ProjectRoot, path)
		}
		stat := ctx.StatDir
		if stat == nil {
			stat = os.Stat
		}
		info, err := stat(path)
		return err == nil && info.IsDir()

	case ProbeExecOnPath:
		lookup := ctx.LookPath
		if lookup == nil {
			lookup = exec.LookPath
		}
		_, err := lookup(p.Name)
		return err == nil

	case ProbeShellExitZero:
		run := ctx.RunShell
		if run == nil {
			run = defaultShellRun
		}
		return run(p.Cmd) == nil

	default:
		return false
	}
}

// AgentDetection is the per-agent result of running every probe in its entry.
type AgentDetection struct {
	AgentID  string
	Detected bool
	// Probes lists each probe and its individual result. The agent is
	// detected iff at least one probe succeeded.
	Probes []ProbeResult
}

// ProbeResult records the outcome of a single probe.
type ProbeResult struct {
	Probe    Probe
	Detected bool
}

// DetectAll evaluates every agent in the manifest against ctx.
func (m *Manifest) DetectAll(ctx ProbeContext) []AgentDetection {
	out := make([]AgentDetection, 0, len(m.Agents))
	for _, a := range m.Agents {
		det := AgentDetection{AgentID: a.ID}
		for _, p := range a.Detect {
			ok := EvaluateProbe(p, ctx)
			det.Probes = append(det.Probes, ProbeResult{Probe: p, Detected: ok})
			if ok {
				det.Detected = true
			}
		}
		out = append(out, det)
	}
	return out
}
