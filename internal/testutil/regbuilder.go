// Package testutil holds shared fixtures and helpers for tests across the
// installer codebase. Production code MUST NOT import this package.
package testutil

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/falabella/fdh/pkg/bundle"
	"github.com/stretchr/testify/require"
)

// SkillSpec describes a skill the test wants to publish into a fixture registry.
type SkillSpec struct {
	Namespace   string
	Name        string
	Version     string
	Description string
	OwnerTeam   string
	Tags        []string
	// Files maps bundle-relative paths to file contents. The map MUST
	// contain a "SKILL.md" entry with valid frontmatter; helpers do not
	// inject one for you so tests can intentionally produce broken bundles.
	Files map[string]string
}

// BuildRegistry creates a complete on-disk registry under root containing
// every skill in specs. It returns once the registry is fully populated.
// The registry layout matches the skill-bundle-and-registry spec:
//
//	root/index.json
//	root/skills/<ns>/<name>/manifest.json
//	root/skills/<ns>/<name>/versions/<ver>/bundle/{...}
//	root/skills/<ns>/<name>/versions/<ver>/bundle.tar.gz
//	root/skills/<ns>/<name>/versions/<ver>/bundle.sha256
func BuildRegistry(t *testing.T, root string, specs []SkillSpec) {
	t.Helper()
	require.NoError(t, os.MkdirAll(root, 0o755))

	indexEntries := []map[string]any{}

	for _, s := range specs {
		ns := s.Namespace
		name := s.Name
		version := s.Version
		skillDir := filepath.Join(root, "skills", ns, name)
		versionDir := filepath.Join(skillDir, "versions", version)
		bundleDir := filepath.Join(versionDir, "bundle")

		require.NoError(t, os.MkdirAll(bundleDir, 0o755))

		// Write bundle files.
		for rel, content := range s.Files {
			p := filepath.Join(bundleDir, filepath.FromSlash(rel))
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		}

		// Compute canonical hash.
		b, err := bundle.Load(bundleDir)
		require.NoError(t, err, "load fixture bundle %s/%s", ns, name)
		hash, err := b.Hash()
		require.NoError(t, err)

		// Produce bundle.tar.gz containing a top-level "bundle/" directory.
		tarPath := filepath.Join(versionDir, "bundle.tar.gz")
		require.NoError(t, writeTarGz(tarPath, bundleDir, "bundle"))

		require.NoError(t, os.WriteFile(
			filepath.Join(versionDir, "bundle.sha256"),
			[]byte(hash+"  bundle.tar.gz\n"), 0o644))

		require.NoError(t, os.WriteFile(
			filepath.Join(versionDir, "changelog.md"),
			[]byte("Initial fixture release.\n"), 0o644))

		require.NoError(t, os.WriteFile(
			filepath.Join(versionDir, "scan-report.json"),
			[]byte(`{"status":"pass","findings":[]}`), 0o644))

		// Write the per-skill manifest.json.
		manifestBytes, _ := json.MarshalIndent(map[string]any{
			"schema_version": 1,
			"namespace":      ns,
			"name":           name,
			"description":    s.Description,
			"owner_team":     s.OwnerTeam,
			"tags":           s.Tags,
			"latest":         version,
			"versions": []map[string]any{
				{
					"version":      version,
					"content_hash": hash,
					"published_at": "2026-05-22T12:00:00Z",
					"published_by": "fixture@test",
					"scan_status":  "pass",
				},
			},
		}, "", "  ")
		require.NoError(t, os.WriteFile(
			filepath.Join(skillDir, "manifest.json"),
			manifestBytes, 0o644))

		// Maintain a README pointer.
		require.NoError(t, os.WriteFile(
			filepath.Join(skillDir, "README.md"),
			[]byte("# "+name+"\n\n"+s.Description+"\n"), 0o644))

		indexEntries = append(indexEntries, map[string]any{
			"namespace":      ns,
			"name":           name,
			"description":    s.Description,
			"owner_team":     s.OwnerTeam,
			"tags":           s.Tags,
			"latest_version": version,
			"latest_hash":    hash,
			"scan_status":    "pass",
		})
	}

	indexBytes, _ := json.MarshalIndent(map[string]any{
		"schema_version": 1,
		"registry":       "file://" + filepath.ToSlash(root),
		"skills":         indexEntries,
	}, "", "  ")
	require.NoError(t, os.WriteFile(filepath.Join(root, "index.json"), indexBytes, 0o644))
}

// SHA256OfFile returns the lowercase hex SHA-256 of the named file.
func SHA256OfFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeTarGz packs srcDir into outPath, prefixing every entry's name with
// prefix so the archive contains exactly one top-level directory (matching
// the layout the GitRegistry's bundle resolver expects).
func writeTarGz(outPath, srcDir, prefix string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	return filepath.Walk(srcDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			// emit the prefix directory entry
			hdr := &tar.Header{
				Name:     prefix + "/",
				Mode:     0o755,
				Typeflag: tar.TypeDir,
			}
			return tw.WriteHeader(hdr)
		}
		name := prefix + "/" + filepath.ToSlash(rel)
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = name
		// Normalize times so hashes (of the tar) are stable across test runs.
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
		defer file.Close()
		_, err = io.Copy(tw, file)
		return err
	})
}

// FixtureSKILLMD returns the canonical SKILL.md body used in many tests.
func FixtureSKILLMD(name, description string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("name: " + name + "\n")
	sb.WriteString("description: " + description + "\n")
	sb.WriteString("---\n\n")
	sb.WriteString("# " + name + "\n\n")
	sb.WriteString(description + "\n")
	return sb.String()
}
