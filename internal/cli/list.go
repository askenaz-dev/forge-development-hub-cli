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

	"github.com/forge/fdh/pkg/adapters"
	"github.com/forge/fdh/pkg/provenance"
	"github.com/spf13/cobra"
)

// ListedSkill is the JSON shape per row emitted by `list --json`.
type ListedSkill struct {
	Skill        string   `json:"skill"`
	Namespace    string   `json:"namespace"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Source       string   `json:"source"`
	Scope        string   `json:"scope"`
	Path         string   `json:"path"`
	TargetAgents []string `json:"target_agents"`
	ContentHash  string   `json:"content_hash"`
}

func newListCmd(info BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List installed skills across detected agent directories",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd, args, info)
		},
	}
	cmd.Flags().StringSlice("agent", nil, "limit to specific agents (may be repeated)")
	cmd.Flags().String("scope", "all", "scope to list: user|project|all")
	return cmd
}

func runList(cmd *cobra.Command, args []string, info BuildInfo) error {
	verbose, _ := cmd.PersistentFlags().GetBool("verbose")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rc, err := buildRunContext(ctx, info, verbose)
	if err != nil {
		return err
	}

	scopeStr, _ := cmd.Flags().GetString("scope")
	scopes := []adapters.Scope{}
	switch strings.ToLower(scopeStr) {
	case "user":
		scopes = []adapters.Scope{adapters.ScopeUser}
	case "project":
		scopes = []adapters.Scope{adapters.ScopeProject}
	case "", "all":
		scopes = []adapters.Scope{adapters.ScopeUser, adapters.ScopeProject}
	default:
		return Errorf(ExitInvalidUsage, "unknown --scope %q (expected user|project|all)", scopeStr)
	}
	requestedAgents, _ := cmd.Flags().GetStringSlice("agent")

	rows := collectInstalled(rc, scopes, requestedAgents)

	if outputMode(cmd) == "json" {
		return emitJSON(cmd.OutOrStdout(), rows)
	}
	return printListTable(cmd.OutOrStdout(), rows)
}

// collectInstalled walks the directories declared by the adapter map and
// returns one row per skill+path combination. Missing/corrupt sidecars
// produce a row with source="unknown".
func collectInstalled(rc *runContext, scopes []adapters.Scope, requestedAgents []string) []ListedSkill {
	// Build a set of unique directories we should look at.
	type pathOrigin struct {
		path   string
		scope  adapters.Scope
		agents []string
	}
	type key struct{ path, scope string }
	seen := map[key]*pathOrigin{}
	order := []key{}

	for _, scope := range scopes {
		for _, agent := range rc.Adapters.Agents {
			if len(requestedAgents) > 0 && !contains(requestedAgents, agent.ID) {
				continue
			}
			var raws []string
			if scope == adapters.ScopeUser {
				raws = agent.Paths.User
			} else {
				if rc.ProjectRoot == "" {
					continue
				}
				raws = agent.Paths.Project
			}
			for _, raw := range raws {
				// Strip the "<name>/" suffix so we get the parent skills/ dir
				// to walk, not the skill-specific dir.
				template := raw
				if strings.Contains(template, "<name>") {
					template = strings.TrimSuffix(template, "<name>/")
					template = strings.TrimSuffix(template, "<name>")
				}
				expanded, err := adapters.ExpandPath(template, rc.HomeDir, rc.ProjectRoot, "")
				if err != nil {
					continue
				}
				k := key{path: expanded, scope: string(scope)}
				if entry, ok := seen[k]; ok {
					if !contains(entry.agents, agent.ID) {
						entry.agents = append(entry.agents, agent.ID)
					}
				} else {
					seen[k] = &pathOrigin{path: expanded, scope: scope, agents: []string{agent.ID}}
					order = append(order, k)
				}
			}
		}
	}

	var rows []ListedSkill
	for _, k := range order {
		origin := seen[k]
		entries, err := os.ReadDir(origin.path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			skillDir := filepath.Join(origin.path, e.Name())
			meta, err := provenance.ReadSidecar(skillDir)
			if err != nil || meta.SchemaVersion == 0 {
				// Sidecar missing or corrupt: emit an "unknown" row with what
				// we can infer from the directory itself.
				rows = append(rows, ListedSkill{
					Skill:        "unknown/" + e.Name(),
					Name:         e.Name(),
					Version:      "unknown",
					Source:       "unknown",
					Scope:        string(origin.scope),
					Path:         skillDir,
					TargetAgents: append([]string(nil), origin.agents...),
				})
				continue
			}
			rows = append(rows, ListedSkill{
				Skill:        meta.Namespace + "/" + meta.Name,
				Namespace:    meta.Namespace,
				Name:         meta.Name,
				Version:      meta.Version,
				Source:       meta.Registry,
				Scope:        meta.Scope,
				Path:         meta.Path,
				TargetAgents: meta.TargetAgents,
				ContentHash:  meta.ContentHash,
			})
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Skill != rows[j].Skill {
			return rows[i].Skill < rows[j].Skill
		}
		return rows[i].Path < rows[j].Path
	})
	return rows
}

func printListTable(w io.Writer, rows []ListedSkill) error {
	if len(rows) == 0 {
		fmt.Fprintln(w, "No installed skills found.")
		return nil
	}
	headers := []string{"SKILL", "VERSION", "SCOPE", "AGENTS", "PATH"}
	tbl := make([][]string, 0, len(rows))
	for _, r := range rows {
		tbl = append(tbl, []string{r.Skill, r.Version, r.Scope, joinAgents(r.TargetAgents), r.Path})
	}
	return printTable(w, headers, tbl)
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
