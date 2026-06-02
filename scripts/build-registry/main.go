// build-registry is the real registry producer: it converts a forge
// Development Hub source checkout (hub/registry.yaml + the four primitive
// directories) into a *built registry* the CLI consumer (GitRegistry /
// HTTPRegistry) can read — `index.json` at the root plus, per component,
// `<kind-plural>/<namespace>/<name>/manifest.json` and a versioned
// `bundle.tar.gz` + `bundle.sha256`.
//
// Unlike scripts/build-fixture-registry (which seeds synthetic skills for
// local installer tests), this reads REAL hub content and is what the
// hub's CI publishes to the consumer-facing registry-dist branch.
//
// Usage:
//
//	go run ./scripts/build-registry <hub-dir> <dest-dir>
//
//	<hub-dir>   a forge-development-hub checkout (contains hub/registry.yaml)
//	<dest-dir>  output directory for the built registry (created if absent)
//
// Content hashing uses bundle.HashDir, the loader-free canonical hash that
// matches (*bundle.Bundle).Hash for every kind — so the digest the producer
// writes equals the one the consumer recomputes after extraction.
package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/forge/fdh/pkg/bundle"
	"github.com/forge/fdh/pkg/hubregistry"
	"github.com/forge/fdh/pkg/registry"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: build-registry <hub-dir> <dest-dir>")
		os.Exit(2)
	}
	hubDir := os.Args[1]
	dest := os.Args[2]

	if err := run(hubDir, dest); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run(hubDir, dest string) error {
	raw, catalogRel, err := readCatalog(hubDir)
	if err != nil {
		return err
	}
	reg, err := hubregistry.Parse(raw, func(line string) { fmt.Fprintln(os.Stderr, "[hub]", line) })
	if err != nil {
		return fmt.Errorf("parse %s: %w", catalogRel, err)
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("mkdir dest: %w", err)
	}

	// A single, stable publish timestamp for this build, so re-running the
	// producer on unchanged content produces byte-identical manifests.
	publishedAt := time.Now().UTC().Format(time.RFC3339)

	idx := registry.Index{SchemaVersion: 2, Registry: "git:forge-development-hub"}

	for _, c := range reg.Components {
		kindPlural := hubregistry.KindDir(c.Kind)
		if kindPlural == "" {
			return fmt.Errorf("component %q has unknown kind %q", c.Name, c.Kind)
		}
		ns := deriveNamespace(c.OwnerTeam)
		version := c.Version
		if version == "" {
			version = "1.0.0"
		}

		srcDir := filepath.Join(hubDir, filepath.FromSlash(c.Path))
		if info, err := os.Stat(srcDir); err != nil || !info.IsDir() {
			return fmt.Errorf("component %q (%s): source dir %s not found", c.Name, c.Kind, srcDir)
		}

		hash, err := bundle.HashDir(srcDir)
		if err != nil {
			return fmt.Errorf("hash %s: %w", srcDir, err)
		}

		compDir := filepath.Join(dest, kindPlural, ns, c.Name)
		versionDir := filepath.Join(compDir, "versions", version)
		if err := os.MkdirAll(versionDir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", versionDir, err)
		}

		// Bundle: tar the source dir under a "bundle/" prefix (the consumer
		// extracts that single top-level dir and renames it to the component
		// name before verifying the hash).
		tarPath := filepath.Join(versionDir, "bundle.tar.gz")
		if err := writeTarGz(tarPath, srcDir, "bundle"); err != nil {
			return fmt.Errorf("tar %s: %w", srcDir, err)
		}
		if err := os.WriteFile(filepath.Join(versionDir, "bundle.sha256"),
			[]byte(hash+"  bundle.tar.gz\n"), 0o644); err != nil {
			return err
		}

		manifest := registry.Manifest{
			SchemaVersion: 1,
			Namespace:     ns,
			Name:          c.Name,
			Description:   c.Description,
			OwnerTeam:     c.OwnerTeam,
			Tags:          c.Tags,
			Latest:        version,
			Versions: []registry.Version{{
				Version:     version,
				ContentHash: hash,
				PublishedAt: publishedAt,
				PublishedBy: "registry-producer",
				ScanStatus:  "none",
				Status:      registry.StatusActive,
			}},
		}
		if err := writeJSON(filepath.Join(compDir, "manifest.json"), manifest); err != nil {
			return err
		}

		idx.Components = append(idx.Components, registry.IndexEntry{
			Kind:          c.Kind,
			Namespace:     ns,
			Name:          c.Name,
			Description:   c.Description,
			OwnerTeam:     c.OwnerTeam,
			Tags:          c.Tags,
			LatestVersion: version,
			LatestHash:    hash,
			ScanStatus:    "none",
		})

		fmt.Printf("published %-5s %s/%s@%s  hash=%s\n", c.Kind, ns, c.Name, version, hash[:12])
	}

	if err := writeJSON(filepath.Join(dest, "index.json"), idx); err != nil {
		return err
	}

	fmt.Printf("\nBuilt registry at %s (%d components)\n", dest, len(idx.Components))
	return nil
}

// readCatalog finds hub/registry.yaml (v2) in the hub checkout, falling back
// to the legacy skills/registry.yaml mirror.
func readCatalog(hubDir string) ([]byte, string, error) {
	for _, rel := range []string{
		filepath.Join("hub", "registry.yaml"),
		filepath.Join("skills", "registry.yaml"),
	} {
		abs := filepath.Join(hubDir, rel)
		if b, err := os.ReadFile(abs); err == nil {
			return b, filepath.ToSlash(rel), nil
		}
	}
	return nil, "", fmt.Errorf("no catalog at %s/hub/registry.yaml", hubDir)
}

// deriveNamespace mirrors the portal's owner_team → namespace slug so the
// built registry and the portal agree on namespaces.
func deriveNamespace(ownerTeam string) string {
	s := strings.ToLower(strings.TrimSpace(ownerTeam))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// writeTarGz archives srcDir under the given top-level prefix. Mirrors the
// fixture builder's archiver so bundles extract identically.
func writeTarGz(outPath, srcDir, prefix string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz := gzip.NewWriter(f)
	defer func() { _ = gz.Close() }()
	tw := tar.NewWriter(gz)
	defer func() { _ = tw.Close() }()

	return filepath.Walk(srcDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return tw.WriteHeader(&tar.Header{Name: prefix + "/", Mode: 0o755, Typeflag: tar.TypeDir})
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = prefix + "/" + filepath.ToSlash(rel)
		hdr.ModTime = info.ModTime()
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		file, err := os.Open(p)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		_, err = io.Copy(tw, file)
		return err
	})
}
