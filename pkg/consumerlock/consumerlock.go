// Package consumerlock defines the consumer-side `.fdh/lock.yaml`
// shape and operations:
//
//   - Build: assemble a Lock from a list of resolved components plus
//     hub_commit / resolved_at / resolved_from_harness.
//   - Write: serialize to disk with byte-deterministic output (LF,
//     no BOM, fixed key order, alphabetical component order).
//   - Read: decode + KnownFields strict check.
//   - Diff: compare manifest expansion against a lock to determine
//     whether `--frozen` should pass or fail.
//
// Boundaries: stdlib + yaml.v3 + pkg/consumermanifest + pkg/managed.
// MUST NOT import internal/cli or pkg/adapters.
package consumerlock

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/forge/fdh/pkg/consumermanifest"
	"github.com/forge/fdh/pkg/managed"
)

// SupportedSchemaVersion is the only lock schema this fdh release
// understands.
const SupportedSchemaVersion = 1

// Filename is the lock's path relative to the consumer repo root.
var Filename = filepath.Join(".fdh", "lock.yaml")

// Lock is the resolved snapshot.
//
// ResolvedFromHarness records which hub harness produced this
// resolution (informational only — the expanded component lists are
// the reproducibility source of truth). resolvedFromProfileLegacy
// decodes the pre-rename `resolved_from_profile` key so old locks read
// without error; Read normalizes it into ResolvedFromHarness and
// clears it, so Write only ever emits `resolved_from_harness`.
type Lock struct {
	SchemaVersion             int         `yaml:"schema_version"`
	HubCommit                 string      `yaml:"hub_commit"`
	ResolvedAt                time.Time   `yaml:"resolved_at"`
	ResolvedFromHarness       string      `yaml:"resolved_from_harness,omitempty"`
	ResolvedFromProfileLegacy string      `yaml:"resolved_from_profile,omitempty"` // deprecated; read-only
	Skills                    []LockEntry `yaml:"skills,omitempty"`
	Rules                     []LockEntry `yaml:"rules,omitempty"`
	Agents                    []LockEntry `yaml:"agents,omitempty"`
	Hooks                     []LockEntry `yaml:"hooks,omitempty"`
}

// LockEntry is one resolved component recorded in the lock.
type LockEntry struct {
	Name      string `yaml:"name"`
	Version   string `yaml:"version,omitempty"`
	Path      string `yaml:"path"`
	Integrity string `yaml:"integrity,omitempty"`
}

// Build assembles a Lock from resolved components plus snapshot
// metadata. Always sorts entries alphabetically by name within each
// kind for byte-determinism.
func Build(resolved []consumermanifest.ResolvedComponent, hubCommit string, resolvedAt time.Time, fromHarness string) *Lock {
	l := &Lock{
		SchemaVersion:       SupportedSchemaVersion,
		HubCommit:           hubCommit,
		ResolvedAt:          resolvedAt.UTC(),
		ResolvedFromHarness: fromHarness,
	}
	for _, r := range resolved {
		entry := LockEntry{Name: r.Name}
		if r.HubEntry != nil {
			// Prefer the resolved version (after constraint
			// satisfaction) over the catalog's raw version. They
			// match in the single-version hubregistry path; they may
			// differ once the wire-protocol multi-version path lands.
			if r.ResolvedVersion != "" {
				entry.Version = r.ResolvedVersion
			} else {
				entry.Version = r.HubEntry.Version
			}
			entry.Path = r.HubEntry.Path
			// content-hash field on the hub manifest acts as integrity
			// for the lock; if the catalog doesn't carry one, leave
			// the integrity blank — diff will skip integrity check.
		}
		switch r.Kind {
		case managed.KindSkill:
			l.Skills = append(l.Skills, entry)
		case managed.KindRule:
			l.Rules = append(l.Rules, entry)
		case managed.KindAgent:
			l.Agents = append(l.Agents, entry)
		case managed.KindHook:
			l.Hooks = append(l.Hooks, entry)
		}
	}
	sortEntries(l.Skills)
	sortEntries(l.Rules)
	sortEntries(l.Agents)
	sortEntries(l.Hooks)
	return l
}

func sortEntries(in []LockEntry) {
	sort.Slice(in, func(i, j int) bool { return in[i].Name < in[j].Name })
}

// Write serializes the Lock to <rootDir>/.fdh/lock.yaml byte-
// deterministically: indent 2 spaces, LF endings, no BOM, no trailing
// whitespace. Atomic via tmp+rename.
func Write(rootDir string, l *Lock) error {
	if l == nil {
		return errors.New("consumerlock.Write: nil lock")
	}
	body, err := marshalDeterministic(l)
	if err != nil {
		return err
	}
	dir := filepath.Join(rootDir, ".fdh")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("consumerlock.Write: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(rootDir, Filename)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("consumerlock.Write: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("consumerlock.Write: rename %s: %w", path, err)
	}
	return nil
}

// marshalDeterministic produces the canonical YAML bytes for a Lock.
// yaml.v3 already orders fields by struct declaration; the receiver
// then strips CRLF (defensive) and trailing whitespace.
func marshalDeterministic(l *Lock) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(l); err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("consumerlock: encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("consumerlock: close encoder: %w", err)
	}
	body := buf.String()
	body = strings.ReplaceAll(body, "\r\n", "\n")
	// Strip trailing whitespace on each line.
	var out strings.Builder
	for _, line := range strings.Split(body, "\n") {
		out.WriteString(strings.TrimRight(line, " \t"))
		out.WriteString("\n")
	}
	// Collapse the trailing extra LF that the loop adds back.
	final := strings.TrimRight(out.String(), "\n") + "\n"
	return []byte(final), nil
}

// Read reads and decodes <rootDir>/.fdh/lock.yaml. Returns
// os.ErrNotExist when the lock is missing; callers should treat
// that as "no lock yet" rather than a hard error.
func Read(rootDir string) (*Lock, error) {
	path := filepath.Join(rootDir, Filename)
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return decode(body)
}

func decode(body []byte) (*Lock, error) {
	var l Lock
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	if err := dec.Decode(&l); err != nil {
		return nil, fmt.Errorf("consumerlock: decode: %w", err)
	}
	if l.SchemaVersion != SupportedSchemaVersion {
		return nil, fmt.Errorf("consumerlock: schema_version %d not supported (this fdh supports %d)",
			l.SchemaVersion, SupportedSchemaVersion)
	}
	// Normalize the deprecated `resolved_from_profile` key so a legacy
	// lock reads cleanly and any rewrite emits `resolved_from_harness`.
	if l.ResolvedFromProfileLegacy != "" {
		if l.ResolvedFromHarness == "" {
			l.ResolvedFromHarness = l.ResolvedFromProfileLegacy
		}
		l.ResolvedFromProfileLegacy = ""
	}
	return &l, nil
}

// Divergence describes one mismatch between manifest expansion and a
// lock. Status: "missing" (manifest expects, lock lacks), "extra"
// (lock has, manifest does not request), "integrity" (matched name
// but integrity differs).
type Divergence struct {
	Name     string
	Kind     string
	Status   string
	Expected string
	Actual   string
}

func (d Divergence) String() string {
	switch d.Status {
	case "missing":
		return fmt.Sprintf("missing from lock: %s/%s", d.Kind, d.Name)
	case "extra":
		return fmt.Sprintf("lock has %s/%s but manifest does not request it", d.Kind, d.Name)
	case "integrity":
		return fmt.Sprintf("integrity mismatch for %s/%s: lock=%s, expected=%s", d.Kind, d.Name, d.Actual, d.Expected)
	default:
		return fmt.Sprintf("divergence(%s) for %s/%s", d.Status, d.Kind, d.Name)
	}
}

// Diff compares a manifest expansion against a lock and reports
// divergences. The optional integrityProvider lets the caller supply
// the expected integrity per (name, kind) so the diff can verify it
// against the lock's recorded value. Pass nil to skip integrity check.
func Diff(resolved []consumermanifest.ResolvedComponent, l *Lock, integrityProvider func(name, kind string) (string, bool)) []Divergence {
	type key struct{ name, kind string }

	want := map[key]consumermanifest.ResolvedComponent{}
	for _, r := range resolved {
		want[key{r.Name, r.Kind}] = r
	}

	have := map[key]LockEntry{}
	for _, e := range l.Skills {
		have[key{e.Name, managed.KindSkill}] = e
	}
	for _, e := range l.Rules {
		have[key{e.Name, managed.KindRule}] = e
	}
	for _, e := range l.Agents {
		have[key{e.Name, managed.KindAgent}] = e
	}
	for _, e := range l.Hooks {
		have[key{e.Name, managed.KindHook}] = e
	}

	var out []Divergence
	for k := range want {
		entry, ok := have[k]
		if !ok {
			out = append(out, Divergence{Name: k.name, Kind: k.kind, Status: "missing"})
			continue
		}
		if integrityProvider != nil {
			expected, hasExpected := integrityProvider(k.name, k.kind)
			if hasExpected && expected != "" && entry.Integrity != "" && expected != entry.Integrity {
				out = append(out, Divergence{
					Name: k.name, Kind: k.kind, Status: "integrity",
					Expected: expected, Actual: entry.Integrity,
				})
			}
		}
	}
	for k := range have {
		if _, ok := want[k]; !ok {
			out = append(out, Divergence{Name: k.name, Kind: k.kind, Status: "extra"})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Empty exists so callers can construct an empty Lock when they want
// to write one with the right schema_version etc.
func Empty() *Lock { return &Lock{SchemaVersion: SupportedSchemaVersion} }

// _ avoids unused-import warnings in stripped-down test builds.
var _ = io.Discard
