package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BuildDeterministicTarball produces a gzipped tar archive of srcDir whose
// bytes are reproducible across builds, OSes, and locales. It is intended
// for serving registry bundles over HTTP with stable ETag/CDN caching.
//
// publishedAt becomes every entry's ModTime; the zero value collapses to
// time.Unix(0, 0).UTC(). Uid/Gid are forced to 0 and Uname/Gname to "".
// Mode is normalized to 0644 for regular files (or 0755 if the executable
// bit is set) and 0755 for directories. Entries are walked in lexicographic
// order of their forward-slash relative paths.
//
// Files and directories whose leaf name starts with "." are skipped to
// match bundle.Bundle.Hash()'s walk behavior, so the canonical content
// hash computed over the extracted directory equals the hash computed over
// the source — independent of whether dotfiles exist on the publisher.
//
// Symlinks and other non-regular entries cause an error; registry bundles
// are expected to be regular-file trees.
//
// Returned hash is the hex SHA-256 of the gzipped bytes; it is intended
// for HTTP ETag / CDN cache keys, NOT for canonical content verification.
// Use bundle.Load(srcDir).Hash() for the canonical content hash that
// pkg/registry.HTTPRegistry verifies against bundle.sha256.
func BuildDeterministicTarball(srcDir string, publishedAt time.Time) ([]byte, string, error) {
	absRoot, err := filepath.Abs(srcDir)
	if err != nil {
		return nil, "", fmt.Errorf("resolve srcDir: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, "", fmt.Errorf("stat srcDir: %w", err)
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("srcDir is not a directory: %s", absRoot)
	}

	mtime := publishedAt.UTC()
	if publishedAt.IsZero() {
		mtime = time.Unix(0, 0).UTC()
	}

	entries, err := collectTarEntries(absRoot)
	if err != nil {
		return nil, "", err
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].relPath < entries[j].relPath
	})

	format := tar.FormatUSTAR
	for _, e := range entries {
		if len(e.relPath) > 100 {
			format = tar.FormatPAX
			break
		}
	}

	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.DefaultCompression)
	if err != nil {
		return nil, "", fmt.Errorf("gzip writer: %w", err)
	}
	gz.Header.Name = ""
	gz.Header.Comment = ""
	gz.Header.ModTime = time.Time{}
	gz.Header.OS = 255 // unknown — avoids embedding the build host's OS byte

	tw := tar.NewWriter(gz)

	for _, e := range entries {
		name := e.relPath
		if e.isDir {
			name = e.relPath + "/"
		}
		hdr := &tar.Header{
			Name:    name,
			Mode:    e.mode,
			ModTime: mtime,
			Uid:     0,
			Gid:     0,
			Uname:   "",
			Gname:   "",
			Format:  format,
		}
		if e.isDir {
			hdr.Typeflag = tar.TypeDir
		} else {
			hdr.Typeflag = tar.TypeReg
			hdr.Size = e.size
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, "", fmt.Errorf("write header %s: %w", e.relPath, err)
		}
		if !e.isDir && e.size > 0 {
			if err := copyFileInto(tw, e.full, e.size); err != nil {
				return nil, "", fmt.Errorf("copy %s: %w", e.relPath, err)
			}
		}
	}

	if err := tw.Close(); err != nil {
		return nil, "", fmt.Errorf("tar close: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, "", fmt.Errorf("gzip close: %w", err)
	}

	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:]), nil
}

type tarEntry struct {
	relPath string
	isDir   bool
	mode    int64
	size    int64
	full    string
}

func collectTarEntries(root string) ([]tarEntry, error) {
	var entries []tarEntry
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if p == root {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("unsupported_entry: symlinks not supported: %s", rel)
		}

		fi, err := d.Info()
		if err != nil {
			return err
		}
		if !fi.IsDir() && !fi.Mode().IsRegular() {
			return fmt.Errorf("unsupported_entry: %s is not a regular file", rel)
		}

		var mode int64
		switch {
		case fi.IsDir():
			mode = 0o755
		case fi.Mode()&0o111 != 0:
			mode = 0o755
		default:
			mode = 0o644
		}

		entries = append(entries, tarEntry{
			relPath: rel,
			isDir:   fi.IsDir(),
			mode:    mode,
			size:    fi.Size(),
			full:    p,
		})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return entries, nil
}

func copyFileInto(dst io.Writer, path string, expected int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	n, err := io.Copy(dst, f)
	if err != nil {
		return err
	}
	if n != expected {
		return fmt.Errorf("size mismatch: hdr=%d wrote=%d", expected, n)
	}
	return nil
}
