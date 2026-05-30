package adapters

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/forge/fdh/pkg/managed"
)

// RuleAdapter materializes a `rule` component into the convention of
// a single AI-agent ecosystem. Mirrors the SkillAdapter pattern.
type RuleAdapter interface {
	Agent() string
	TargetPath(name, projectRoot, homeDir string, scope Scope) (string, error)
	Install(srcDir string, opts InstallOpts) (InstallResult, error)
}

// rulePaths returns the per-agent install path for a rule.
// All current ecosystems follow `<agent-root>/rules/<name>.md`.
type ruleAdapter struct {
	agent string
	root  func(homeDir, projectRoot string, scope Scope) (string, error)
}

func (r ruleAdapter) Agent() string { return r.agent }

func (r ruleAdapter) TargetPath(name, projectRoot, homeDir string, scope Scope) (string, error) {
	root, err := r.root(homeDir, projectRoot, scope)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name+".md"), nil
}

func (r ruleAdapter) Install(srcDir string, opts InstallOpts) (InstallResult, error) {
	target, err := r.TargetPath(opts.SkillName, opts.ProjectRoot, opts.HomeDir, opts.Scope)
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
		Agent:       r.agent,
		SkillName:   opts.SkillName,
		TargetPath:  target,
		MarkerPath:  markerPath,
		ContentHash: hash,
	}

	if existing, err := managed.Read(markerPath); err == nil && existing.ContentHash == hash && !opts.Overwrite {
		result.Skipped = true
		return result, nil
	}

	// Read the RULE.md body and write it as <name>.md.
	body, err := readRuleMD(srcDir)
	if err != nil {
		return result, fmt.Errorf("read RULE.md: %w", err)
	}
	if !opts.DryRun {
		if err := writeFileAtomic(target, body, 0o644); err != nil {
			return result, fmt.Errorf("write rule: %w", err)
		}
	}
	result.FilesWritten = []string{basename}

	marker := managed.Marker{
		Name:           opts.SkillName,
		Kind:           managed.KindRule,
		Version:        opts.HubVersion,
		HubCommit:      opts.HubCommit,
		InstalledByFDH: opts.InstalledByFDH,
		SourcePath:     "rules/" + opts.SkillName,
		ContentHash:    hash,
		Agent:          r.agent,
	}
	if !opts.DryRun {
		if _, err := managed.Write(dir, basename, marker, true); err != nil {
			return result, fmt.Errorf("write marker: %w", err)
		}
	}
	return result, nil
}

// readRuleMD reads `<srcDir>/RULE.md`.
func readRuleMD(srcDir string) ([]byte, error) {
	return os.ReadFile(filepath.Join(srcDir, "RULE.md"))
}

// AllRuleAdapters returns the four shipped rule adapters.
func AllRuleAdapters() []RuleAdapter {
	return []RuleAdapter{
		ruleAdapter{agent: "claude-code", root: ruleRoot(".claude", "rules")},
		ruleAdapter{agent: "codex", root: ruleRoot(".codex", "rules")},
		ruleAdapter{agent: "copilot", root: ruleRoot(".github", "rules")},
		ruleAdapter{agent: "opencode", root: ruleRoot(".opencode", "rules")},
	}
}

// RuleAdapterByID returns the adapter for an agent id, or nil.
func RuleAdapterByID(id string) RuleAdapter {
	for _, a := range AllRuleAdapters() {
		if a.Agent() == id {
			return a
		}
	}
	return nil
}

func ruleRoot(projectSubdir, kindSubdir string) func(homeDir, projectRoot string, scope Scope) (string, error) {
	return func(homeDir, projectRoot string, scope Scope) (string, error) {
		switch scope {
		case ScopeUser:
			if homeDir == "" {
				return "", fmt.Errorf("rule adapter: user scope requires homeDir")
			}
			return filepath.Join(homeDir, projectSubdir, kindSubdir), nil
		case ScopeProject:
			if projectRoot == "" {
				return "", fmt.Errorf("rule adapter: project scope requires projectRoot")
			}
			return filepath.Join(projectRoot, projectSubdir, kindSubdir), nil
		default:
			return "", fmt.Errorf("rule adapter: unknown scope %q", scope)
		}
	}
}
