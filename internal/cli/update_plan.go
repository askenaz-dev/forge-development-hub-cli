package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/forge/fdh/pkg/adapters"
	"github.com/forge/fdh/pkg/hubregistry"
)

// UpdatePlanAction is the per-(skill, agent) decision the planner
// produces. Each action ends up in one of three buckets at apply
// time: applied, skipped, or failed.
type UpdatePlanAction struct {
	Skill  string `json:"skill"`
	Agent  string `json:"agent"`

	// HubCommit at install time, recorded in the marker.
	InstalledHubCommit string `json:"installed_hub_commit"`

	// HubCommit at the time of this plan computation.
	CurrentHubCommit string `json:"current_hub_commit"`

	// Action is the verb that describes what apply would do:
	// "refresh"  — hub moved forward; rewrite content.
	// "up-to-date" — installed marker matches hub HEAD; nothing to do.
	// "drift"    — local content_hash diverges from marker; skip unless --force.
	// "vanished" — hub no longer ships this skill; warn, no removal.
	Action string `json:"action"`

	// Reason holds any human-readable explanation worth surfacing.
	Reason string `json:"reason,omitempty"`

	// Files lists added/modified/deleted relative paths between the
	// installed content and the hub HEAD for this skill. Populated
	// only when Action == "refresh".
	Files UpdateFileDiff `json:"files,omitempty"`
}

// UpdateFileDiff is a coarse-grained summary of file-level changes
// between the installed copy and the hub HEAD. Matches the spec's
// "list of files added/modified/deleted, not full content diff".
type UpdateFileDiff struct {
	Added    []string `json:"added,omitempty"`
	Modified []string `json:"modified,omitempty"`
	Deleted  []string `json:"deleted,omitempty"`
}

// planUpdates computes the diff between installed and the hub for
// the (filtered) intersection of (installed, hub.Skills). Both
// skillFilter and agentFilter are inclusive — empty means "all".
// driftLocal=true forces refresh even when the local content_hash
// has drifted (the --force flag).
func planUpdates(
	ctx context.Context,
	installed []InstalledSkill,
	reg *hubregistry.Registry,
	skillFilter, agentFilter map[string]bool,
	forceDrift bool,
) ([]UpdatePlanAction, error) {
	var plan []UpdatePlanAction
	for _, inst := range installed {
		if len(skillFilter) > 0 && !skillFilter[inst.Skill] {
			continue
		}
		if len(agentFilter) > 0 && !agentFilter[inst.Agent] {
			continue
		}
		entry := reg.SkillByName(inst.Skill)
		if entry == nil {
			plan = append(plan, UpdatePlanAction{
				Skill: inst.Skill, Agent: inst.Agent,
				InstalledHubCommit: inst.Marker.HubCommit,
				CurrentHubCommit:   reg.HubCommit,
				Action:             "vanished",
				Reason:             "skill no longer present in hub registry",
			})
			continue
		}
		// Drift detection: recompute content hash and compare to marker.
		dirToHash := inst.InstallDir
		if hash, err := adapters.ComputeContentHash(dirToHash); err == nil {
			if hash != inst.Marker.ContentHash {
				if !forceDrift {
					plan = append(plan, UpdatePlanAction{
						Skill: inst.Skill, Agent: inst.Agent,
						InstalledHubCommit: inst.Marker.HubCommit,
						CurrentHubCommit:   reg.HubCommit,
						Action:             "drift",
						Reason:             "local edits detected; pass --force to overwrite",
					})
					continue
				}
			}
		}
		if inst.Marker.HubCommit != "" && inst.Marker.HubCommit == reg.HubCommit {
			plan = append(plan, UpdatePlanAction{
				Skill: inst.Skill, Agent: inst.Agent,
				InstalledHubCommit: inst.Marker.HubCommit,
				CurrentHubCommit:   reg.HubCommit,
				Action:             "up-to-date",
			})
			continue
		}
		// Compute file-level diff so the user can see what's changing.
		hubSrc, err := reg.FetchSkill(ctx, inst.Skill)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", inst.Skill, err)
		}
		diff, err := computeFileDiff(inst.InstallDir, hubSrc, inst.Agent)
		if err != nil {
			return nil, err
		}
		plan = append(plan, UpdatePlanAction{
			Skill: inst.Skill, Agent: inst.Agent,
			InstalledHubCommit: inst.Marker.HubCommit,
			CurrentHubCommit:   reg.HubCommit,
			Action:             "refresh",
			Files:              diff,
		})
	}
	sort.Slice(plan, func(i, j int) bool {
		if plan[i].Skill != plan[j].Skill {
			return plan[i].Skill < plan[j].Skill
		}
		return plan[i].Agent < plan[j].Agent
	})
	return plan, nil
}

// computeFileDiff lists files present in only one of installed/hub
// and files present in both whose content hashes differ. For flat
// adapters the diff is meaningful only for the single prompt file.
func computeFileDiff(installedDir, hubDir, agent string) (UpdateFileDiff, error) {
	hubFiles, err := listFiles(hubDir, excludeMarker)
	if err != nil {
		return UpdateFileDiff{}, err
	}
	installedFiles, err := listFiles(installedDir, excludeMarker)
	if err != nil {
		return UpdateFileDiff{}, err
	}
	hubSet := mapBy(hubFiles, func(f fileInfo) string { return f.rel })
	insSet := mapBy(installedFiles, func(f fileInfo) string { return f.rel })

	var diff UpdateFileDiff
	for _, f := range hubFiles {
		if other, ok := insSet[f.rel]; !ok {
			diff.Added = append(diff.Added, f.rel)
		} else if other.hash != f.hash {
			diff.Modified = append(diff.Modified, f.rel)
		}
	}
	for _, f := range installedFiles {
		if _, ok := hubSet[f.rel]; !ok {
			diff.Deleted = append(diff.Deleted, f.rel)
		}
	}
	sort.Strings(diff.Added)
	sort.Strings(diff.Modified)
	sort.Strings(diff.Deleted)
	_ = agent // kept for future per-agent gating; currently unused.
	return diff, nil
}

type fileInfo struct {
	rel  string
	hash string
}

func listFiles(root string, skip func(name string) bool) ([]fileInfo, error) {
	var out []fileInfo
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if skip(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		hash, err := hashFileLF(p)
		if err != nil {
			return err
		}
		out = append(out, fileInfo{rel: filepath.ToSlash(rel), hash: hash})
		return nil
	})
	return out, err
}

func excludeMarker(name string) bool {
	return adapters.IsMarkerFilename(name)
}

func hashFileLF(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	// Reuse the package's LF normalisation by routing through
	// ComputeContentHash on a single-file directory would be heavier
	// than needed; instead do the normalisation inline.
	out := make([]byte, 0, len(body))
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '\r' {
			out = append(out, '\n')
			if i+1 < len(body) && body[i+1] == '\n' {
				i++
			}
			continue
		}
		out = append(out, c)
	}
	return fmt.Sprintf("%x", sumSHA256(out)), nil
}

// sumSHA256 is a tiny helper so this file doesn't import crypto in
// the few callers that need it.
func sumSHA256(b []byte) []byte {
	h := newSHA256()
	_, _ = h.Write(b)
	return h.Sum(nil)
}

func mapBy[T any](xs []T, key func(T) string) map[string]T {
	out := make(map[string]T, len(xs))
	for _, x := range xs {
		out[key(x)] = x
	}
	return out
}

// keep one stable label for the result strings the planner emits.
const (
	planRefresh    = "refresh"
	planUpToDate   = "up-to-date"
	planDrift      = "drift"
	planVanished   = "vanished"
	_              = planRefresh + planUpToDate + planDrift + planVanished // silence unused
)

// flagSetToMap converts a CSV/string slice from cobra into a set
// for fast filter checks.
func flagSetToMap(values []string) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]bool, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			out[v] = true
		}
	}
	return out
}
