package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/forge/fdh/pkg/registry"
)

// writeHub lays out a minimal forge-development-hub checkout with hub/registry.yaml
// and the given component source dirs. components maps "<kind>/<name>" → the file
// contents to drop in that component's directory (filename → body).
func writeHub(t *testing.T, comps map[string]map[string]string) string {
	t.Helper()
	hub := t.TempDir()

	catalog := "schema_version: 2\nhub_version: test\ncomponents:\n"
	for key, files := range comps {
		kind, name := splitKey(t, key)
		plural := kind + "s"
		catalog += "  - name: " + name + "\n"
		catalog += "    kind: " + kind + "\n"
		catalog += "    description: " + name + " component\n"
		catalog += "    owner_team: platform\n"
		catalog += "    version: 1.0.0\n"
		catalog += "    agents_supported: [claude-code]\n"
		catalog += "    path: " + plural + "/" + name + "\n"

		dir := filepath.Join(hub, plural, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for fn, body := range files {
			if err := os.WriteFile(filepath.Join(dir, fn), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	if err := os.MkdirAll(filepath.Join(hub, "hub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hub, "hub", "registry.yaml"), []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	return hub
}

func splitKey(t *testing.T, key string) (kind, name string) {
	t.Helper()
	for i := 0; i < len(key); i++ {
		if key[i] == '/' {
			return key[:i], key[i+1:]
		}
	}
	t.Fatalf("bad component key %q (want <kind>/<name>)", key)
	return "", ""
}

func readIndex(t *testing.T, dest string) registry.Index {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dest, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var idx registry.Index
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatal(err)
	}
	return idx
}

func scanStatusOf(idx registry.Index, name string) string {
	for _, e := range idx.Components {
		if e.Name == name {
			return e.ScanStatus
		}
	}
	return ""
}

// TestProducer_ScanStatusPopulated covers tasks 1.1, 1.2, 1.5: pass/warn/fail
// land in index.json (not the old "none" sentinel).
func TestProducer_ScanStatusPopulated(t *testing.T) {
	hub := writeHub(t, map[string]map[string]string{
		"skill/clean":  {"SKILL.md": "# clean\nnothing to see here\n"},
		"skill/leaky":  {"SKILL.md": "token: ghp_abcdefghijklmnopqrstuvwxyz1234567890\n"},
		"rule/warnish": {"RULE.md": "jwt: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N\n"},
	})
	dest := t.TempDir()

	if err := run(hub, dest); err != nil {
		t.Fatalf("run: %v", err)
	}

	idx := readIndex(t, dest)
	if got := scanStatusOf(idx, "clean"); got != "pass" {
		t.Errorf("clean scan_status = %q, want pass", got)
	}
	if got := scanStatusOf(idx, "leaky"); got != "fail" {
		t.Errorf("leaky scan_status = %q, want fail", got)
	}
	if got := scanStatusOf(idx, "warnish"); got != "warn" {
		t.Errorf("warnish scan_status = %q, want warn", got)
	}

	// The per-version manifest carries the same verdict.
	data, err := os.ReadFile(filepath.Join(dest, "skills", "platform", "leaky", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m registry.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m.Versions[0].ScanStatus != "fail" {
		t.Errorf("leaky manifest version scan_status = %q, want fail", m.Versions[0].ScanStatus)
	}
}

// TestProducer_ScanCacheReusesPriorVerdict covers task 1.3: a second build over
// the same dest reuses the cached verdict by content hash.
func TestProducer_ScanCacheReusesPriorVerdict(t *testing.T) {
	hub := writeHub(t, map[string]map[string]string{
		"skill/clean": {"SKILL.md": "# clean\nok\n"},
	})
	dest := t.TempDir()

	if err := run(hub, dest); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first := readIndex(t, dest)

	// Re-run: the prior index.json seeds the content-hash cache. The verdict
	// for the unchanged bundle is identical.
	if err := run(hub, dest); err != nil {
		t.Fatalf("second run: %v", err)
	}
	second := readIndex(t, dest)

	if scanStatusOf(first, "clean") != scanStatusOf(second, "clean") {
		t.Errorf("verdict changed across builds: %q → %q",
			scanStatusOf(first, "clean"), scanStatusOf(second, "clean"))
	}
	if scanStatusOf(second, "clean") != "pass" {
		t.Errorf("cached verdict = %q, want pass", scanStatusOf(second, "clean"))
	}
}

// TestProducer_PriorNoneIsNotCached covers the self-healing aspect of task 1.4:
// a prior "none" verdict is not reused, so a transient scan failure is retried.
func TestProducer_PriorNoneIsNotCached(t *testing.T) {
	dest := t.TempDir()
	// Seed a prior index.json where the component recorded "none".
	prior := registry.Index{
		SchemaVersion: 2,
		Registry:      "git:forge-development-hub",
		Components: []registry.IndexEntry{{
			Kind: "skill", Namespace: "platform", Name: "clean",
			LatestVersion: "1.0.0", LatestHash: "deadbeef", ScanStatus: "none",
		}},
	}
	data, _ := json.MarshalIndent(prior, "", "  ")
	if err := os.WriteFile(filepath.Join(dest, "index.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	cache := loadPriorScanStatus(dest)
	if _, ok := cache["deadbeef"]; ok {
		t.Errorf("prior 'none' verdict should not seed the cache, but got %v", cache)
	}
}
