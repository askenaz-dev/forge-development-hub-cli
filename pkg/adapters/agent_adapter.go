package adapters

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/forge/fdh/pkg/managed"
)

// AgentAdapter materializes a `kind: agent` component. Only Claude
// Code currently ships an agent adapter — other ecosystems either
// don't expose an "agents" abstraction or are still spec'd.
type AgentAdapter interface {
	Agent() string
	TargetPath(name, projectRoot, homeDir string, scope Scope) (string, error)
	Install(srcDir string, opts InstallOpts) (InstallResult, error)
}

type claudeAgentAdapter struct{}

func (claudeAgentAdapter) Agent() string { return "claude-code" }

func (claudeAgentAdapter) TargetPath(name, projectRoot, homeDir string, scope Scope) (string, error) {
	file := name + ".md"
	switch scope {
	case ScopeUser:
		if homeDir == "" {
			return "", fmt.Errorf("claude-agent: user scope requires homeDir")
		}
		return filepath.Join(homeDir, ".claude", "agents", file), nil
	case ScopeProject:
		if projectRoot == "" {
			return "", fmt.Errorf("claude-agent: project scope requires projectRoot")
		}
		return filepath.Join(projectRoot, ".claude", "agents", file), nil
	}
	return "", fmt.Errorf("claude-agent: unknown scope %q", scope)
}

func (a claudeAgentAdapter) Install(srcDir string, opts InstallOpts) (InstallResult, error) {
	target, err := a.TargetPath(opts.SkillName, opts.ProjectRoot, opts.HomeDir, opts.Scope)
	if err != nil {
		return InstallResult{}, err
	}
	hash, err := ComputeContentHash(srcDir)
	if err != nil {
		return InstallResult{}, fmt.Errorf("hash source: %w", err)
	}
	dir := filepath.Dir(target)
	basename := filepath.Base(target)
	markerPath := filepath.Join(dir, managed.FilenameFor(basename, true))

	result := InstallResult{
		Agent:       a.Agent(),
		SkillName:   opts.SkillName,
		TargetPath:  target,
		MarkerPath:  markerPath,
		ContentHash: hash,
	}
	if existing, err := managed.Read(markerPath); err == nil && existing.ContentHash == hash && !opts.Overwrite {
		result.Skipped = true
		return result, nil
	}
	body, err := os.ReadFile(filepath.Join(srcDir, "AGENT.md"))
	if err != nil {
		return result, fmt.Errorf("read AGENT.md: %w", err)
	}
	if !opts.DryRun {
		if err := writeFileAtomic(target, body, 0o644); err != nil {
			return result, fmt.Errorf("write agent: %w", err)
		}
	}
	result.FilesWritten = []string{basename}

	marker := managed.Marker{
		Name:           opts.SkillName,
		Kind:           managed.KindAgent,
		Version:        opts.HubVersion,
		HubCommit:      opts.HubCommit,
		InstalledByFDH: opts.InstalledByFDH,
		SourcePath:     "agents/" + opts.SkillName,
		ContentHash:    hash,
		Agent:          a.Agent(),
	}
	if !opts.DryRun {
		if _, err := managed.Write(dir, basename, marker, true); err != nil {
			return result, fmt.Errorf("write marker: %w", err)
		}
	}
	return result, nil
}

// AllAgentAdapters returns the shipped agent adapters (Claude only
// for now).
func AllAgentAdapters() []AgentAdapter {
	return []AgentAdapter{claudeAgentAdapter{}}
}

// AgentAdapterByID returns the adapter for the given agent id, or nil.
func AgentAdapterByID(id string) AgentAdapter {
	for _, a := range AllAgentAdapters() {
		if a.Agent() == id {
			return a
		}
	}
	return nil
}
