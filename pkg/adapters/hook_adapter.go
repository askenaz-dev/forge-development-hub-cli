package adapters

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/forge/fdh/pkg/managed"
)

// HookAdapter installs a `kind: hook` component. Hooks are agent-
// specific glue; today only Claude Code's `.claude/settings.json`
// shape is supported. Future ecosystems plug in via additional
// adapters here.
type HookAdapter interface {
	Agent() string
	Install(srcDir string, opts InstallOpts) (InstallResult, error)
}

type claudeHookAdapter struct{}

func (claudeHookAdapter) Agent() string { return "claude-code" }

// HookConfig is the YAML/JSON shape of a hook's `hook.json` file.
type hookConfig struct {
	Name    string                 `json:"name"`
	Trigger string                 `json:"trigger"`
	Config  map[string]interface{} `json:"config"`
}

// Install merges the hook's config block into the project's
// `.claude/settings.json` under a managed section marked with
// `_fdh_managed: <name>`. Developer-added blocks are preserved.
func (a claudeHookAdapter) Install(srcDir string, opts InstallOpts) (InstallResult, error) {
	hash, err := ComputeContentHash(srcDir)
	if err != nil {
		return InstallResult{}, fmt.Errorf("hash source: %w", err)
	}
	settingsPath := filepath.Join(opts.ProjectRoot, ".claude", "settings.json")
	target := settingsPath // Hooks don't have a per-name path; the settings file is the install target.

	// Read existing settings.
	var settings map[string]interface{}
	if body, err := os.ReadFile(settingsPath); err == nil {
		_ = json.Unmarshal(body, &settings)
	}
	if settings == nil {
		settings = map[string]interface{}{}
	}

	// Read the hook's config.
	cfgBytes, err := os.ReadFile(filepath.Join(srcDir, "hook.json"))
	if err != nil {
		return InstallResult{}, fmt.Errorf("read hook.json: %w", err)
	}
	var hc hookConfig
	if err := json.Unmarshal(cfgBytes, &hc); err != nil {
		return InstallResult{}, fmt.Errorf("parse hook.json: %w", err)
	}

	// Apply: place the hook config under `hooks._fdh_managed_<name>`.
	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = map[string]interface{}{}
	}
	key := "_fdh_managed_" + opts.SkillName
	entry := map[string]interface{}{
		"_fdh_managed": opts.SkillName,
		"trigger":      hc.Trigger,
		"config":       hc.Config,
	}
	hooks[key] = entry
	settings["hooks"] = hooks

	if !opts.DryRun {
		if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
			return InstallResult{}, fmt.Errorf("mkdir settings: %w", err)
		}
		body, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return InstallResult{}, fmt.Errorf("marshal settings: %w", err)
		}
		if err := writeFileAtomic(settingsPath, append(body, '\n'), 0o644); err != nil {
			return InstallResult{}, fmt.Errorf("write settings: %w", err)
		}
	}

	// Marker as sibling to settings.json.
	markerDir := filepath.Dir(settingsPath)
	marker := managed.Marker{
		Name:           opts.SkillName,
		Kind:           managed.KindHook,
		Version:        opts.HubVersion,
		HubCommit:      opts.HubCommit,
		InstalledByFDH: opts.InstalledByFDH,
		SourcePath:     "hooks/" + opts.SkillName,
		ContentHash:    hash,
		Agent:          a.Agent(),
	}
	markerBasename := "settings.json"
	if !opts.DryRun {
		if _, err := managed.Write(markerDir, markerBasename, marker, true); err != nil {
			return InstallResult{}, fmt.Errorf("write marker: %w", err)
		}
	}
	markerPath := filepath.Join(markerDir, managed.FilenameFor(markerBasename, true))

	return InstallResult{
		Agent:       a.Agent(),
		SkillName:   opts.SkillName,
		TargetPath:  target,
		MarkerPath:  markerPath,
		ContentHash: hash,
	}, nil
}

// AllHookAdapters returns the shipped hook adapters.
func AllHookAdapters() []HookAdapter {
	return []HookAdapter{claudeHookAdapter{}}
}

// HookAdapterByID returns the adapter for the given agent id, or nil.
func HookAdapterByID(id string) HookAdapter {
	for _, a := range AllHookAdapters() {
		if a.Agent() == id {
			return a
		}
	}
	return nil
}
