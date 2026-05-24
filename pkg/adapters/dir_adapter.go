package adapters

import (
	"fmt"
	"path/filepath"
)

// ClaudeCodeAdapter installs a skill as a directory copy under the
// Claude Code conventional skill root.
//
//   - user scope:    ~/.claude/skills/<name>/
//   - project scope: <project>/.claude/skills/<name>/
//
// The `.skill-version` marker is placed inside the copied directory.
type ClaudeCodeAdapter struct{}

func (ClaudeCodeAdapter) Agent() string             { return "claude-code" }
func (ClaudeCodeAdapter) SupportsSubresources() bool { return true }

func (ClaudeCodeAdapter) TargetPath(skillName, projectRoot, homeDir string, scope Scope) (string, error) {
	switch scope {
	case ScopeUser:
		if homeDir == "" {
			return "", fmt.Errorf("claude-code: user scope requires homeDir")
		}
		return filepath.Join(homeDir, ".claude", "skills", skillName), nil
	case ScopeProject:
		if projectRoot == "" {
			return "", fmt.Errorf("claude-code: project scope requires projectRoot")
		}
		return filepath.Join(projectRoot, ".claude", "skills", skillName), nil
	default:
		return "", fmt.Errorf("claude-code: unknown scope %q", scope)
	}
}

func (a ClaudeCodeAdapter) Install(srcDir string, opts InstallOpts) (InstallResult, error) {
	return directoryInstall(a.Agent(), srcDir, opts, a.TargetPath)
}

// CodexAdapter mirrors ClaudeCodeAdapter for the OpenAI Codex agent.
// Layout:
//
//   - user scope:    ~/.codex/skills/<name>/
//   - project scope: <project>/.codex/skills/<name>/
type CodexAdapter struct{}

func (CodexAdapter) Agent() string              { return "codex" }
func (CodexAdapter) SupportsSubresources() bool { return true }

func (CodexAdapter) TargetPath(skillName, projectRoot, homeDir string, scope Scope) (string, error) {
	switch scope {
	case ScopeUser:
		if homeDir == "" {
			return "", fmt.Errorf("codex: user scope requires homeDir")
		}
		return filepath.Join(homeDir, ".codex", "skills", skillName), nil
	case ScopeProject:
		if projectRoot == "" {
			return "", fmt.Errorf("codex: project scope requires projectRoot")
		}
		return filepath.Join(projectRoot, ".codex", "skills", skillName), nil
	default:
		return "", fmt.Errorf("codex: unknown scope %q", scope)
	}
}

func (a CodexAdapter) Install(srcDir string, opts InstallOpts) (InstallResult, error) {
	return directoryInstall(a.Agent(), srcDir, opts, a.TargetPath)
}

// directoryInstall is the shared implementation for directory-based
// agents. It:
//  1. Computes the canonical content hash of srcDir.
//  2. Reads any existing marker at target. If hashes match, returns
//     Skipped=true without writing.
//  3. Copies srcDir → target (overwriting iff opts.Overwrite).
//  4. Writes `.skill-version` inside target.
type targetPathFn func(skillName, projectRoot, homeDir string, scope Scope) (string, error)

func directoryInstall(agent, srcDir string, opts InstallOpts, tp targetPathFn) (InstallResult, error) {
	target, err := tp(opts.SkillName, opts.ProjectRoot, opts.HomeDir, opts.Scope)
	if err != nil {
		return InstallResult{}, err
	}
	hash, err := ComputeContentHash(srcDir)
	if err != nil {
		return InstallResult{}, fmt.Errorf("hash source: %w", err)
	}
	markerPath := filepath.Join(target, MarkerName(agent, opts.SkillName))

	result := InstallResult{
		Agent:       agent,
		SkillName:   opts.SkillName,
		TargetPath:  target,
		MarkerPath:  markerPath,
		ContentHash: hash,
	}

	if existing, err := LoadMarker(markerPath); err == nil && existing.ContentHash == hash && !opts.Overwrite {
		result.Skipped = true
		return result, nil
	}

	written, err := copyTree(srcDir, target, opts.Overwrite, opts.DryRun)
	if err != nil {
		return result, fmt.Errorf("copy tree: %w", err)
	}
	result.FilesWritten = written

	marker := SkillVersionMarker{
		Name:           opts.SkillName,
		HubVersion:     opts.HubVersion,
		HubCommit:      opts.HubCommit,
		InstalledByFDH: opts.InstalledByFDH,
		ContentHash:    hash,
		Agent:          agent,
	}
	body, err := MarshalMarker(marker)
	if err != nil {
		return result, err
	}
	if !opts.DryRun {
		if err := writeFileAtomic(markerPath, body, 0o644); err != nil {
			return result, fmt.Errorf("write marker: %w", err)
		}
	}
	return result, nil
}

