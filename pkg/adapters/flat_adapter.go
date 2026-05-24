package adapters

import (
	"fmt"
	"path/filepath"
)

// CopilotAdapter installs a skill as a flat `.prompt.md` file. GitHub
// Copilot reads prompts from `.github/prompts/<name>.prompt.md`
// (project scope) or `~/.config/github-copilot/prompts/` (user
// scope).
//
// Because the destination is a single file, any subresources the hub
// skill ships (the `references/` folder, attachments) cannot be
// represented faithfully. The adapter surfaces a warning so the
// wizard can show it.
type CopilotAdapter struct{}

func (CopilotAdapter) Agent() string              { return "copilot" }
func (CopilotAdapter) SupportsSubresources() bool { return false }

func (CopilotAdapter) TargetPath(skillName, projectRoot, homeDir string, scope Scope) (string, error) {
	file := skillName + ".prompt.md"
	switch scope {
	case ScopeUser:
		if homeDir == "" {
			return "", fmt.Errorf("copilot: user scope requires homeDir")
		}
		return filepath.Join(homeDir, ".config", "github-copilot", "prompts", file), nil
	case ScopeProject:
		if projectRoot == "" {
			return "", fmt.Errorf("copilot: project scope requires projectRoot")
		}
		return filepath.Join(projectRoot, ".github", "prompts", file), nil
	default:
		return "", fmt.Errorf("copilot: unknown scope %q", scope)
	}
}

func (a CopilotAdapter) Install(srcDir string, opts InstallOpts) (InstallResult, error) {
	return flatInstall(a.Agent(), srcDir, opts, a.TargetPath)
}

// OpenCodeAdapter installs a skill as a flat command file:
//
//   - user scope:    ~/.config/opencode/commands/<name>.md
//   - project scope: <project>/.opencode/commands/<name>.md
//
// Same caveat as Copilot: subresources are not portable; the adapter
// emits a warning when the source had any.
type OpenCodeAdapter struct{}

func (OpenCodeAdapter) Agent() string              { return "opencode" }
func (OpenCodeAdapter) SupportsSubresources() bool { return false }

func (OpenCodeAdapter) TargetPath(skillName, projectRoot, homeDir string, scope Scope) (string, error) {
	file := skillName + ".md"
	switch scope {
	case ScopeUser:
		if homeDir == "" {
			return "", fmt.Errorf("opencode: user scope requires homeDir")
		}
		return filepath.Join(homeDir, ".config", "opencode", "commands", file), nil
	case ScopeProject:
		if projectRoot == "" {
			return "", fmt.Errorf("opencode: project scope requires projectRoot")
		}
		return filepath.Join(projectRoot, ".opencode", "commands", file), nil
	default:
		return "", fmt.Errorf("opencode: unknown scope %q", scope)
	}
}

func (a OpenCodeAdapter) Install(srcDir string, opts InstallOpts) (InstallResult, error) {
	return flatInstall(a.Agent(), srcDir, opts, a.TargetPath)
}

// flatInstall is the shared implementation for single-file agents.
// It copies SKILL.md to the target path and writes the marker as a
// sibling file (`.skill-version-<name>`).
func flatInstall(agent, srcDir string, opts InstallOpts, tp targetPathFn) (InstallResult, error) {
	target, err := tp(opts.SkillName, opts.ProjectRoot, opts.HomeDir, opts.Scope)
	if err != nil {
		return InstallResult{}, err
	}
	body, err := readSkillMD(srcDir)
	if err != nil {
		return InstallResult{}, err
	}
	hash, err := ComputeContentHash(srcDir)
	if err != nil {
		return InstallResult{}, fmt.Errorf("hash source: %w", err)
	}

	markerName := MarkerName(agent, opts.SkillName)
	markerPath := filepath.Join(filepath.Dir(target), markerName)

	result := InstallResult{
		Agent:       agent,
		SkillName:   opts.SkillName,
		TargetPath:  target,
		MarkerPath:  markerPath,
		ContentHash: hash,
	}
	if hasSubresources(srcDir) {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("skill %s has subresources that are not portable to %s — only SKILL.md was installed", opts.SkillName, agent))
	}

	if existing, err := LoadMarker(markerPath); err == nil && existing.ContentHash == hash && !opts.Overwrite {
		result.Skipped = true
		return result, nil
	}

	if !opts.DryRun {
		if err := writeFileAtomic(target, body, 0o644); err != nil {
			return result, fmt.Errorf("write prompt: %w", err)
		}
	}
	result.FilesWritten = []string{filepath.Base(target)}

	marker := SkillVersionMarker{
		Name:           opts.SkillName,
		HubVersion:     opts.HubVersion,
		HubCommit:      opts.HubCommit,
		InstalledByFDH: opts.InstalledByFDH,
		ContentHash:    hash,
		Agent:          agent,
	}
	markerBody, err := MarshalMarker(marker)
	if err != nil {
		return result, err
	}
	if !opts.DryRun {
		if err := writeFileAtomic(markerPath, markerBody, 0o644); err != nil {
			return result, fmt.Errorf("write marker: %w", err)
		}
	}
	return result, nil
}
