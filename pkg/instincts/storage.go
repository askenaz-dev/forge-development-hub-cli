package instincts

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// dirPerms is the permission mode for ~/.fdh/instincts/.
// On Windows os.Chmod is best-effort; the dir's ACL is inherited from $HOME.
const dirPerms = 0o700

// HomeDir resolves the FDH home directory, honoring FDH_HOME for tests.
func HomeDir() (string, error) {
	if v := os.Getenv("FDH_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".fdh"), nil
}

// InstinctsDir is the directory under FDH_HOME that holds the per-instinct YAML files.
func InstinctsDir() (string, error) {
	h, err := HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "instincts"), nil
}

// EnsureDir creates ~/.fdh/instincts/ with permissions 0700 on Unix.
// Idempotent.
func EnsureDir() (string, error) {
	dir, err := InstinctsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, dirPerms); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	if runtime.GOOS != "windows" {
		// MkdirAll honors umask on creation; explicit chmod ensures 0700.
		_ = os.Chmod(dir, dirPerms)
	}
	return dir, nil
}

// Path returns the absolute on-disk path of an instinct by ID.
func Path(id string) (string, error) {
	dir, err := InstinctsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id+".yaml"), nil
}

// WriteAtomic encodes the instinct, writes to <id>.yaml.tmp + fsync (Unix),
// then renames to <id>.yaml. A crash mid-write leaves at most a .tmp file
// (cleaned up by Cleanup) but never a partial .yaml.
func WriteAtomic(i *Instinct) error {
	dir, err := EnsureDir()
	if err != nil {
		return err
	}
	encoded, err := i.Encode()
	if err != nil {
		return err
	}
	final := filepath.Join(dir, i.ID+".yaml")
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if runtime.GOOS != "windows" {
		if f, err := os.Open(tmp); err == nil {
			_ = f.Sync()
			_ = f.Close()
		}
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s -> %s: %w", tmp, final, err)
	}
	return nil
}

// Read loads an instinct by ID.
func Read(id string) (*Instinct, error) {
	p, err := Path(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return Decode(data)
}

// Delete removes an instinct by ID. Returns nil if it didn't exist.
func Delete(id string) error {
	p, err := Path(id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// List returns all instinct IDs in storage, sorted ascending. Cleans up any
// stray .tmp files encountered (logged to stderr).
//
// Malformed files (invalid ULID name or unreadable YAML) are skipped with a
// best-effort log to stderr; the caller gets the IDs that parsed cleanly.
func List() ([]string, error) {
	dir, err := InstinctsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Cleanup any stray .tmp files from interrupted writes.
		if strings.HasSuffix(name, ".tmp") {
			_ = os.Remove(filepath.Join(dir, name))
			fmt.Fprintf(os.Stderr, "instincts: cleaned up stray temp file: %s\n", name)
			continue
		}
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}
		id := strings.TrimSuffix(name, ".yaml")
		if !ulidPattern.MatchString(id) {
			fmt.Fprintf(os.Stderr, "instincts: skipping %s (not a valid ULID filename)\n", name)
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// ReadAll returns every readable instinct in storage. Skips malformed entries
// with a best-effort stderr log.
func ReadAll() ([]*Instinct, error) {
	ids, err := List()
	if err != nil {
		return nil, err
	}
	out := make([]*Instinct, 0, len(ids))
	for _, id := range ids {
		i, err := Read(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "instincts: skipping %s: %v\n", id, err)
			continue
		}
		out = append(out, i)
	}
	return out, nil
}

// -----------------------------------------------------------------------------
// ~/.fdh/state.json `instincts:` section
// -----------------------------------------------------------------------------

// StateInstincts is the shape of the `instincts:` section of ~/.fdh/state.json.
//
// Designed to be additive — we read the full state.json, mutate only the
// instincts section, and write it back. Other sections (user_scope_installs,
// hub_cache, projects) are preserved verbatim.
type StateInstincts struct {
	Count       int        `json:"count"`
	LastCapture *time.Time `json:"last_capture,omitempty"`
	LastExport  *time.Time `json:"last_export,omitempty"`
	LastEvolve  *time.Time `json:"last_evolve,omitempty"`
	EvolveRuns  int        `json:"evolve_runs"`
}

func statePath() (string, error) {
	h, err := HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "state.json"), nil
}

// MutateState reads ~/.fdh/state.json (creates it if absent), passes the
// `instincts` sub-map to the mutator, and writes the result back atomically.
//
// The state.json file is treated as opaque except for the `instincts:` key.
// Other top-level keys are preserved.
func MutateState(mutate func(*StateInstincts)) error {
	p, err := statePath()
	if err != nil {
		return err
	}
	var top map[string]any
	if data, err := os.ReadFile(p); err == nil {
		if err := json.Unmarshal(data, &top); err != nil {
			// Corrupt state.json — start a fresh top-level map but preserve nothing.
			fmt.Fprintf(os.Stderr, "instincts: state.json was unreadable JSON, starting fresh: %v\n", err)
			top = map[string]any{"schema_version": 1}
		}
	} else if os.IsNotExist(err) {
		top = map[string]any{"schema_version": 1}
	} else {
		return fmt.Errorf("read state.json: %w", err)
	}

	// Pull the existing instincts section if any.
	var section StateInstincts
	if raw, ok := top["instincts"]; ok {
		if b, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(b, &section)
		}
	}

	mutate(&section)

	top["instincts"] = section

	if _, err := EnsureDir(); err != nil { // ensures ~/.fdh/ exists
		// Non-fatal — state.json may live alongside, not inside, instincts/.
		_ = err
	}
	// Ensure ~/.fdh/ itself exists.
	if h, err := HomeDir(); err == nil {
		_ = os.MkdirAll(h, dirPerms)
	}
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state.json: %w", err)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("write state.json tmp: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename state.json: %w", err)
	}
	return nil
}

// ReadStateInstincts returns the current instincts section (zero value if absent).
func ReadStateInstincts() (StateInstincts, error) {
	p, err := statePath()
	if err != nil {
		return StateInstincts{}, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return StateInstincts{}, nil
		}
		return StateInstincts{}, err
	}
	var top map[string]any
	if err := json.Unmarshal(data, &top); err != nil {
		return StateInstincts{}, nil //nolint:nilerr // corrupt state → treat as empty
	}
	raw, ok := top["instincts"]
	if !ok {
		return StateInstincts{}, nil
	}
	var section StateInstincts
	b, _ := json.Marshal(raw)
	_ = json.Unmarshal(b, &section)
	return section, nil
}

// -----------------------------------------------------------------------------
// Prefix resolution (for `fdh instinct show/edit/delete <id-prefix>`)
// -----------------------------------------------------------------------------

// ResolvePrefix matches a (possibly partial) ULID against the set of stored
// instinct IDs. Returns the full ID on unique match, or an error listing
// candidates on ambiguity / no-match.
func ResolvePrefix(prefix string) (string, error) {
	prefix = strings.ToUpper(prefix)
	if prefix == "" {
		return "", fmt.Errorf("empty prefix")
	}
	ids, err := List()
	if err != nil {
		return "", err
	}
	var matches []string
	for _, id := range ids {
		if strings.HasPrefix(id, prefix) {
			matches = append(matches, id)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no instinct found with prefix %q", prefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous prefix %q; matches: %s", prefix, strings.Join(matches, ", "))
	}
}

// -----------------------------------------------------------------------------
// Bundle export / import format detection
// -----------------------------------------------------------------------------

// BundleFormat is the detected format of an export bundle.
type BundleFormat int

const (
	BundleYAML  BundleFormat = iota // single YAML doc with `instincts: [...]`
	BundleJSON                      // single JSON array
	BundleTarGz                     // tar.gz of <id>.yaml files
	BundleUnknown
)

// DetectBundleFormat picks the bundle format based on the file extension.
func DetectBundleFormat(path string) BundleFormat {
	l := strings.ToLower(path)
	switch {
	case strings.HasSuffix(l, ".yaml") || strings.HasSuffix(l, ".yml"):
		return BundleYAML
	case strings.HasSuffix(l, ".json"):
		return BundleJSON
	case strings.HasSuffix(l, ".tar.gz") || strings.HasSuffix(l, ".tgz"):
		return BundleTarGz
	default:
		return BundleUnknown
	}
}

// BundleDoc is the wrapping object used by YAML and JSON bundle exports.
type BundleDoc struct {
	SchemaVersion int              `json:"schema_version" yaml:"schema_version"`
	ExportedAt    time.Time        `json:"exported_at" yaml:"exported_at"`
	Instincts     []map[string]any `json:"instincts" yaml:"instincts"`
}

// WriteAll dumps an io.Writer with the given bundle in the given format.
//
// For tar.gz callers should use the streaming helpers in cli/instinct.go;
// here we cover the inline yaml/json formats.
func WriteAll(w io.Writer, format BundleFormat, items []*Instinct) error {
	doc := BundleDoc{
		SchemaVersion: SchemaVersion,
		ExportedAt:    time.Now().UTC(),
		Instincts:     make([]map[string]any, 0, len(items)),
	}
	for _, it := range items {
		entry := map[string]any{
			"id":             it.ID,
			"title":          it.Title,
			"confidence":     it.Confidence,
			"domain":         it.Domain,
			"captured_by":    it.CapturedBy,
			"captured_at":    it.CapturedAt.UTC(),
			"context":        it.Context,
			"tags":           it.Tags,
			"related_skills": it.RelatedSkills,
			"body":           it.Body,
		}
		doc.Instincts = append(doc.Instincts, entry)
	}
	switch format {
	case BundleYAML:
		enc := yaml.NewEncoder(w)
		enc.SetIndent(2)
		defer func() { _ = enc.Close() }()
		return enc.Encode(doc)
	case BundleJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	default:
		return fmt.Errorf("WriteAll: unsupported format %v (use ExportTarGz for tar.gz)", format)
	}
}

// ReadAllBundle parses a YAML or JSON bundle into Instinct pointers.
// tar.gz handling lives next to the CLI command because it streams.
func ReadAllBundle(data []byte, format BundleFormat) ([]*Instinct, error) {
	var doc BundleDoc
	switch format {
	case BundleYAML:
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("yaml decode: %w", err)
		}
	case BundleJSON:
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("json decode: %w", err)
		}
	default:
		return nil, fmt.Errorf("ReadAllBundle: unsupported format %v", format)
	}
	out := make([]*Instinct, 0, len(doc.Instincts))
	for idx, entry := range doc.Instincts {
		// Round-trip through YAML to handle nested Context cleanly.
		raw, err := yaml.Marshal(entry)
		if err != nil {
			return nil, fmt.Errorf("entry %d: re-marshal: %w", idx, err)
		}
		// Extract body separately because the on-disk file stores body outside
		// the frontmatter, but bundles put body inside the entry.
		bodyAny := entry["body"]
		bodyStr, _ := bodyAny.(string)
		// Build an Instinct from the frontmatter fields; ignore body via -.
		var i Instinct
		if err := yaml.Unmarshal(raw, &i); err != nil {
			return nil, fmt.Errorf("entry %d: unmarshal: %w", idx, err)
		}
		i.Body = bodyStr
		out = append(out, &i)
	}
	return out, nil
}
