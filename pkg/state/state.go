// Package state implements the per-machine ledger at
// `~/.fdh/state.json` per the `installation-state-ledger` spec.
//
// The ledger records:
//   - user_scope_installs: components installed at user scope, by kind
//   - hub_cache: last hub pull metadata (commit, url, timestamp)
//   - projects: optional, opt-in registry of project paths and their
//     lock_hash / managed_paths / last_install_at
//
// Boundaries: stdlib only. MUST NOT import pkg/adapters, pkg/managed,
// or internal/cli.
package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// SupportedSchemaVersion is the only ledger schema this fdh release
// understands.
const SupportedSchemaVersion = 1

// Filename is the ledger path relative to the user's home dir
// (joined with `.fdh/state.json`).
const Filename = "state.json"

// State is the on-disk JSON shape.
type State struct {
	SchemaVersion     int                     `json:"schema_version"`
	UserScopeInstalls KindBuckets             `json:"user_scope_installs"`
	HubCache          HubCache                `json:"hub_cache"`
	Projects          map[string]ProjectEntry `json:"projects,omitempty"`
}

// KindBuckets holds installs per kind.
type KindBuckets struct {
	Skills []InstallEntry `json:"skills,omitempty"`
	Rules  []InstallEntry `json:"rules,omitempty"`
	Agents []InstallEntry `json:"agents,omitempty"`
	Hooks  []InstallEntry `json:"hooks,omitempty"`
}

// InstallEntry is one user-scope install.
type InstallEntry struct {
	Name        string    `json:"name"`
	Version     string    `json:"version,omitempty"`
	InstalledAt time.Time `json:"installed_at"`
	Path        string    `json:"path"`
}

// HubCache records the last hub-clone state.
type HubCache struct {
	LastPulled time.Time `json:"last_pulled"`
	Commit     string    `json:"commit,omitempty"`
	URL        string    `json:"url,omitempty"`
}

// ProjectEntry is one entry in state.projects.
type ProjectEntry struct {
	LockHash      string    `json:"lock_hash"`
	ManagedPaths  []string  `json:"managed_paths"`
	LastInstallAt time.Time `json:"last_install_at"`
}

// LedgerDir returns the directory holding the ledger
// (`<homeDir>/.fdh`).
func LedgerDir(homeDir string) string { return filepath.Join(homeDir, ".fdh") }

// LedgerPath returns the absolute on-disk path of state.json.
func LedgerPath(homeDir string) string { return filepath.Join(LedgerDir(homeDir), Filename) }

// Load reads the ledger from `<homeDir>/.fdh/state.json`. If the file
// does not exist, returns an empty State (no error) so callers can
// freely Save afterwards.
func Load(homeDir string) (*State, error) {
	if homeDir == "" {
		return nil, errors.New("state.Load: homeDir is empty")
	}
	body, err := os.ReadFile(LedgerPath(homeDir))
	if err != nil {
		if os.IsNotExist(err) {
			return &State{SchemaVersion: SupportedSchemaVersion}, nil
		}
		return nil, err
	}
	var s State
	dec := json.NewDecoder(bytesReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("state.Load: decode %s: %w", LedgerPath(homeDir), err)
	}
	if s.SchemaVersion != SupportedSchemaVersion {
		return nil, fmt.Errorf("state.Load: schema_version %d not supported (this fdh supports %d)", s.SchemaVersion, SupportedSchemaVersion)
	}
	return &s, nil
}

// Save writes the ledger atomically.
func Save(homeDir string, s *State) error {
	if homeDir == "" {
		return errors.New("state.Save: homeDir is empty")
	}
	if s == nil {
		return errors.New("state.Save: nil state")
	}
	if s.SchemaVersion == 0 {
		s.SchemaVersion = SupportedSchemaVersion
	}
	sortBuckets(&s.UserScopeInstalls)
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("state.Save: marshal: %w", err)
	}
	dir := LedgerDir(homeDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("state.Save: mkdir %s: %w", dir, err)
	}
	path := LedgerPath(homeDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("state.Save: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("state.Save: rename %s: %w", path, err)
	}
	return nil
}

// UpsertProject creates or updates a project entry for absPath.
func (s *State) UpsertProject(absPath string, entry ProjectEntry) {
	if s.Projects == nil {
		s.Projects = map[string]ProjectEntry{}
	}
	if entry.LastInstallAt.IsZero() {
		entry.LastInstallAt = time.Now().UTC()
	}
	sort.Strings(entry.ManagedPaths)
	s.Projects[absPath] = entry
}

// RemoveProject deletes a project entry. No-op if absent.
func (s *State) RemoveProject(absPath string) {
	if s.Projects == nil {
		return
	}
	delete(s.Projects, absPath)
}

// SetUserScopeInstall replaces (or inserts) the install entry for
// (name, kind) within user_scope_installs.
func (s *State) SetUserScopeInstall(kind string, e InstallEntry) {
	if e.InstalledAt.IsZero() {
		e.InstalledAt = time.Now().UTC()
	}
	switch kind {
	case "skill":
		s.UserScopeInstalls.Skills = upsertByName(s.UserScopeInstalls.Skills, e)
	case "rule":
		s.UserScopeInstalls.Rules = upsertByName(s.UserScopeInstalls.Rules, e)
	case "agent":
		s.UserScopeInstalls.Agents = upsertByName(s.UserScopeInstalls.Agents, e)
	case "hook":
		s.UserScopeInstalls.Hooks = upsertByName(s.UserScopeInstalls.Hooks, e)
	}
}

// RemoveUserScopeInstall removes an entry by name within a kind.
func (s *State) RemoveUserScopeInstall(kind, name string) {
	switch kind {
	case "skill":
		s.UserScopeInstalls.Skills = removeByName(s.UserScopeInstalls.Skills, name)
	case "rule":
		s.UserScopeInstalls.Rules = removeByName(s.UserScopeInstalls.Rules, name)
	case "agent":
		s.UserScopeInstalls.Agents = removeByName(s.UserScopeInstalls.Agents, name)
	case "hook":
		s.UserScopeInstalls.Hooks = removeByName(s.UserScopeInstalls.Hooks, name)
	}
}

// HashLock computes a SHA-256 of lockBody — used as `lock_hash` in
// project entries.
func HashLock(lockBody []byte) string {
	sum := sha256.Sum256(lockBody)
	return hex.EncodeToString(sum[:])
}

func upsertByName(in []InstallEntry, e InstallEntry) []InstallEntry {
	for i, x := range in {
		if x.Name == e.Name {
			in[i] = e
			return in
		}
	}
	return append(in, e)
}

func removeByName(in []InstallEntry, name string) []InstallEntry {
	out := in[:0]
	for _, x := range in {
		if x.Name == name {
			continue
		}
		out = append(out, x)
	}
	return out
}

func sortBuckets(k *KindBuckets) {
	sort.Slice(k.Skills, func(i, j int) bool { return k.Skills[i].Name < k.Skills[j].Name })
	sort.Slice(k.Rules, func(i, j int) bool { return k.Rules[i].Name < k.Rules[j].Name })
	sort.Slice(k.Agents, func(i, j int) bool { return k.Agents[i].Name < k.Agents[j].Name })
	sort.Slice(k.Hooks, func(i, j int) bool { return k.Hooks[i].Name < k.Hooks[j].Name })
}

// bytesReader wraps a byte slice as an io.Reader for json.NewDecoder.
func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
