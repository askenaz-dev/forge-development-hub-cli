//go:build smoke

// Subprocess smoke test for the portal-api wire endpoints. Builds the
// `fdh-portal-api` binary, starts it pointed at the hub fixture, and
// drives it through the 7 scenarios that Section 11 of the
// extend-portal-api-with-wire-protocol change calls for:
//
//  1. binary starts cleanly with FDH_PORTAL_HUB_PATH set
//  2. GET /v1/index.json returns 200 + Content-Type + ETag
//  3. GET /v1/skills/dx-platform/test-skill/manifest.json returns the manifest
//  4. GET .../bundle.sha256 returns the canonical content hash
//  5. GET .../bundle.tar.gz; extracted-dir hash matches the sidecar
//  6. Two GETs of .../bundle.tar.gz produce byte-identical bodies
//  7. GET with If-None-Match: <etag> returns 304
//
// Run with:
//
//	go test -tags=smoke ./cmd/fdh-portal-api/...
//
// Skipped under the default `go test ./...` to keep the unit suite fast
// and avoid building a binary on every run.
package main_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/internal/testutil"
	"github.com/forge/fdh/pkg/bundle"
	"github.com/forge/fdh/pkg/registry"
)

func TestSmoke_PortalAPI_WireProtocol_FullFlow(t *testing.T) {
	root := repoRoot(t)
	bin := buildBinary(t, root)
	port := pickFreePort(t)
	addr := "127.0.0.1:" + port
	base := "http://" + addr

	env := []string{
		"FDH_PORTAL_API_ADDR=" + addr,
		"FDH_PORTAL_HUB_PATH=" + testutil.HubFixturePath(),
		// LoadConfig requires at least one of these — point at an empty
		// tempdir so the GitRegistry never tries to clone anything.
		"FDH_PORTAL_REGISTRY_LOCAL_PATH=" + t.TempDir(),
		"FDH_PORTAL_REFRESH_INTERVAL=60s",
	}
	for _, kv := range os.Environ() {
		k := strings.SplitN(kv, "=", 2)[0]
		switch strings.ToUpper(k) {
		case "PATH", "SYSTEMROOT", "TEMP", "TMP":
			env = append(env, kv)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = env
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})

	waitForReady(t, base, 10*time.Second)

	client := &http.Client{Timeout: 5 * time.Second}

	// 11.2 — GET /v1/index.json
	idxResp := getOK(t, client, base+"/v1/index.json")
	require.Equal(t, "application/json; charset=utf-8", idxResp.contentType)
	require.NotEmpty(t, idxResp.etag)
	var idx registry.Index
	dec := json.NewDecoder(bytes.NewReader(idxResp.body))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&idx))
	require.Equal(t, 2, idx.SchemaVersion)
	byKind := map[string]registry.IndexEntry{}
	for _, e := range idx.Components {
		byKind[e.Kind] = e
	}
	for _, k := range []string{"skill", "rule", "agent", "hook"} {
		require.Contains(t, byKind, k, "kind %q present in /v1/index.json", k)
	}
	require.Equal(t, "dx-platform", byKind["skill"].Namespace)
	require.Equal(t, "test-skill", byKind["skill"].Name)

	// 11.3 — GET manifest
	manResp := getOK(t, client, base+"/v1/skills/dx-platform/test-skill/manifest.json")
	var man registry.Manifest
	dec2 := json.NewDecoder(bytes.NewReader(manResp.body))
	dec2.DisallowUnknownFields()
	require.NoError(t, dec2.Decode(&man))
	require.Equal(t, "dx-platform", man.Namespace)
	require.Equal(t, "0.1.0", man.Latest)
	require.Len(t, man.Versions, 1)
	manifestHash := man.Versions[0].ContentHash
	require.Len(t, manifestHash, 64)

	// 11.4 — GET bundle.sha256, capture the hash
	sidecarResp := getOK(t, client, base+"/v1/skills/dx-platform/test-skill/versions/0.1.0/bundle.sha256")
	require.Equal(t, "text/plain; charset=utf-8", sidecarResp.contentType)
	sidecarFields := strings.Fields(string(sidecarResp.body))
	require.GreaterOrEqual(t, len(sidecarFields), 2)
	sidecarHash := sidecarFields[0]
	require.Equal(t, "bundle.tar.gz", sidecarFields[1])
	require.Equal(t, manifestHash, sidecarHash,
		"sidecar hash and manifest content_hash must agree")

	// 11.5 — GET bundle.tar.gz; extracted-dir hash matches sidecar.
	tarResp := getOK(t, client, base+"/v1/skills/dx-platform/test-skill/versions/0.1.0/bundle.tar.gz")
	require.Equal(t, "application/gzip", tarResp.contentType)
	require.NotEmpty(t, tarResp.etag)

	extractDir := t.TempDir()
	require.NoError(t, extractTarGzBytes(tarResp.body, extractDir))
	canonicalHash, err := bundle.HashDir(extractDir)
	require.NoError(t, err)
	require.Equal(t, sidecarHash, canonicalHash,
		"canonical content hash of extracted tarball must equal sidecar SHA")

	// 11.6 — Two GETs of bundle.tar.gz produce identical bytes.
	tarResp2 := getOK(t, client, base+"/v1/skills/dx-platform/test-skill/versions/0.1.0/bundle.tar.gz")
	require.True(t, bytes.Equal(tarResp.body, tarResp2.body),
		"bundle.tar.gz must be byte-identical across requests")
	require.Equal(t, tarResp.etag, tarResp2.etag)

	// 11.7 — GET with If-None-Match: <etag> returns 304.
	req, err := http.NewRequest(http.MethodGet, base+"/v1/index.json", nil)
	require.NoError(t, err)
	req.Header.Set("If-None-Match", idxResp.etag)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusNotModified, resp.StatusCode)
	require.Empty(t, body)
}

// --- helpers ---

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	cur := wd
	for {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			t.Fatalf("go.mod not found above %s", wd)
		}
		cur = parent
	}
}

func buildBinary(t *testing.T, root string) string {
	t.Helper()
	outDir := t.TempDir()
	bin := filepath.Join(outDir, "fdh-portal-api")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/fdh-portal-api")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "go build failed: %s", out)
	return bin
}

// pickFreePort asks the OS for a free TCP port and returns it as a string.
// The listener is closed before returning so the test subprocess can bind it.
// There is a tiny race window between close and bind; in practice this is
// fine for a single-test smoke run.
func pickFreePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return formatPort(port)
}

func formatPort(p int) string {
	return strconv.Itoa(p)
}

func waitForReady(t *testing.T, base string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 1 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("portal-api did not become ready within %s", timeout)
}

type httpResult struct {
	body        []byte
	contentType string
	etag        string
}

func getOK(t *testing.T, client *http.Client, url string) httpResult {
	t.Helper()
	resp, err := client.Get(url)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equalf(t, http.StatusOK, resp.StatusCode,
		"GET %s expected 200, got %d: %s", url, resp.StatusCode, string(body))
	return httpResult{
		body:        body,
		contentType: resp.Header.Get("Content-Type"),
		etag:        resp.Header.Get("ETag"),
	}
}

func extractTarGzBytes(data []byte, dst string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}
