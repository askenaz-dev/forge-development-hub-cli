package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/forge/fdh/pkg/adapters"
)

// InstalledSkill is one skill+agent marker found on disk by
// findInstalledSkills. It's the minimal record the update planner
// needs to compute drift and propose changes.
type InstalledSkill struct {
	// Agent is the adapter id ("claude-code", "codex", ...).
	Agent string `json:"agent"`

	// Skill is the kebab-case name from the marker.
	Skill string `json:"skill"`

	// MarkerPath is the absolute path of `.skill-version` or
	// `.skill-version-<name>`.
	MarkerPath string `json:"marker_path"`

	// InstallDir is the directory that contains the installed
	// content. For directory-based agents this is the skill folder;
	// for flat agents it is the directory holding the prompt file.
	InstallDir string `json:"install_dir"`

	// Marker is the deserialised contents of MarkerPath.
	Marker adapters.SkillVersionMarker `json:"marker"`
}

// findInstalledSkills scans the conventional directories of every
// shipped adapter (per AllSkillAdapters) at both user and project
// scope and returns one InstalledSkill per marker discovered.
//
// For directory-based agents the scan looks one level deep under the
// agent's skills/ root for a `.skill-version` file. For flat agents
// the scan looks in the prompts/commands directory for any file
// named `.skill-version-*`.
//
// Errors stopping the walk are propagated; missing directories are
// ignored (the agent simply has no installs in that scope).
func findInstalledSkills(homeDir, projectRoot string) ([]InstalledSkill, error) {
	var out []InstalledSkill
	scopes := []adapters.Scope{adapters.ScopeUser}
	if projectRoot != "" {
		scopes = append(scopes, adapters.ScopeProject)
	}
	for _, adapter := range adapters.AllSkillAdapters() {
		for _, scope := range scopes {
			root, err := adapterScopeRoot(adapter, homeDir, projectRoot, scope)
			if err != nil {
				continue
			}
			found, err := scanScopeRoot(adapter, root)
			if err != nil {
				return nil, fmt.Errorf("scan %s (%s): %w", adapter.Agent(), scope, err)
			}
			out = append(out, found...)
		}
	}
	return out, nil
}

// adapterScopeRoot returns the directory the adapter writes its
// installs into, without the per-skill suffix. For directory-based
// agents that is `~/.claude/skills/` (and similar). For flat agents
// that is `~/.config/github-copilot/prompts/`.
//
// The trick: TargetPath always returns a per-skill path; we compute
// it for a dummy skill name and trim the suffix.
func adapterScopeRoot(a adapters.SkillAdapter, homeDir, projectRoot string, scope adapters.Scope) (string, error) {
	dummy := "_fdh_dummy_"
	full, err := a.TargetPath(dummy, projectRoot, homeDir, scope)
	if err != nil {
		return "", err
	}
	// Directory adapter: full is `<root>/<skill>` so root = dirname.
	// Flat adapter: full is `<root>/<skill>.prompt.md` so root = dirname.
	return filepath.Dir(full), nil
}

func scanScopeRoot(a adapters.SkillAdapter, root string) ([]InstalledSkill, error) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, nil
	}
	if a.SupportsSubresources() {
		return scanDirectoryRoot(a, root)
	}
	return scanFlatRoot(a, root)
}

func scanDirectoryRoot(a adapters.SkillAdapter, root string) ([]InstalledSkill, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []InstalledSkill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		installDir := filepath.Join(root, e.Name())
		markerPath := filepath.Join(installDir, ".skill-version")
		marker, err := adapters.LoadMarker(markerPath)
		if err != nil {
			continue // not our skill
		}
		if marker.Agent != "" && marker.Agent != a.Agent() {
			// Marker belongs to a different agent that happens to
			// reuse this path (e.g. Copilot pointing at .claude).
			continue
		}
		out = append(out, InstalledSkill{
			Agent:      a.Agent(),
			Skill:      marker.Name,
			MarkerPath: markerPath,
			InstallDir: installDir,
			Marker:     marker,
		})
	}
	return out, nil
}

func scanFlatRoot(a adapters.SkillAdapter, root string) ([]InstalledSkill, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []InstalledSkill
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, ".skill-version-") {
			continue
		}
		markerPath := filepath.Join(root, name)
		marker, err := adapters.LoadMarker(markerPath)
		if err != nil {
			continue
		}
		if marker.Agent != "" && marker.Agent != a.Agent() {
			continue
		}
		out = append(out, InstalledSkill{
			Agent:      a.Agent(),
			Skill:      marker.Name,
			MarkerPath: markerPath,
			InstallDir: root,
			Marker:     marker,
		})
	}
	return out, nil
}
