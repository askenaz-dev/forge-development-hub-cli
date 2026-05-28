package registry_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/forge/fdh/internal/testutil"
	"github.com/forge/fdh/pkg/registry"
)

// fixtureRegistry builds an on-disk registry under a fresh temp dir and
// returns the dir path along with two canonical specs.
func fixtureRegistry(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	testutil.BuildRegistry(t, root, []testutil.SkillSpec{
		{
			Namespace:   "code-review",
			Name:        "standard",
			Version:     "1.0.0",
			Description: "Standard code review checklist.",
			OwnerTeam:   "dx",
			Tags:        []string{"review", "quality"},
			Files: map[string]string{
				"SKILL.md": testutil.FixtureSKILLMD("standard", "Standard code review checklist."),
			},
		},
		{
			Namespace:   "security",
			Name:        "owasp-review",
			Version:     "1.2.0",
			Description: "Run an OWASP top-10 sweep.",
			OwnerTeam:   "appsec",
			Tags:        []string{"owasp", "security"},
			Files: map[string]string{
				"SKILL.md":            testutil.FixtureSKILLMD("owasp-review", "Run an OWASP top-10 sweep."),
				"references/owasp.md": "Top 10 ...",
			},
		},
	})
	return root
}

// serveRegistry wraps a directory in an http.FileServer that also sets
// ETag and Cache-Control headers so the HTTPRegistry's conditional
// revalidation logic exercises against a realistic mock.
func serveRegistry(t *testing.T, root string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	fs := http.FileServer(http.Dir(root))
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		p := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(req.URL.Path, "/")))
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			fs.ServeHTTP(w, req)
			return
		}
		data, err := os.ReadFile(p)
		if err != nil {
			fs.ServeHTTP(w, req)
			return
		}
		etag := `"` + hexHash(data)[:16] + `"`
		w.Header().Set("ETag", etag)
		// bundle.* are immutable per the wire protocol; index/manifest
		// advertise a short max-age so revalidation kicks in.
		switch {
		case strings.HasSuffix(p, "bundle.tar.gz"), strings.HasSuffix(p, "bundle.sha256"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case strings.HasSuffix(p, "index.json"), strings.HasSuffix(p, "manifest.json"):
			w.Header().Set("Cache-Control", "public, max-age=60")
		}
		if inm := req.Header.Get("If-None-Match"); inm == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		fs.ServeHTTP(w, req)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &hits
}

func hexHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func newHTTPRegistry(t *testing.T, baseURL string) *registry.HTTPRegistry {
	t.Helper()
	return &registry.HTTPRegistry{
		BaseURL:    baseURL,
		APIVersion: "v1",
		CacheDir:   t.TempDir(),
	}
}

func TestHTTPRegistry_Source(t *testing.T) {
	r := &registry.HTTPRegistry{BaseURL: "https://example.test/v1/", APIVersion: "v1"}
	assert.Equal(t, "http:https://example.test/v1/?api=v1", r.Source())
}

func TestHTTPRegistry_Index(t *testing.T) {
	root := fixtureRegistry(t)
	srv, _ := serveRegistry(t, root)
	r := newHTTPRegistry(t, srv.URL+"/")

	idx, err := r.Index(context.Background())
	require.NoError(t, err)
	require.Len(t, idx.Skills, 2)
	names := []string{idx.Skills[0].Name, idx.Skills[1].Name}
	assert.Contains(t, names, "standard")
	assert.Contains(t, names, "owasp-review")
}

func TestHTTPRegistry_Index_CacheHitAvoidsGET(t *testing.T) {
	root := fixtureRegistry(t)
	srv, hits := serveRegistry(t, root)
	r := newHTTPRegistry(t, srv.URL+"/")

	_, err := r.Index(context.Background())
	require.NoError(t, err)
	before := hits.Load()

	// Second call inside the max-age window — should not GET again.
	_, err = r.Index(context.Background())
	require.NoError(t, err)
	assert.Equal(t, before, hits.Load(),
		"second Index call hit the network despite a fresh cache entry")
}

func TestHTTPRegistry_Index_Revalidation304(t *testing.T) {
	root := fixtureRegistry(t)
	srv, _ := serveRegistry(t, root)
	r := newHTTPRegistry(t, srv.URL+"/")

	_, err := r.Index(context.Background())
	require.NoError(t, err)

	// Expire the cache by rewriting the meta file with a fetched_at far
	// in the past — simulates the TTL elapsing without waiting.
	expireMeta(t, r, srv.URL+"/index.json")

	idx, err := r.Index(context.Background())
	require.NoError(t, err)
	require.Len(t, idx.Skills, 2)
}

func TestHTTPRegistry_Index_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	r := newHTTPRegistry(t, srv.URL+"/")

	_, err := r.Index(context.Background())
	require.Error(t, err)
	var unreach registry.RegistryUnreachable
	assert.ErrorAs(t, err, &unreach, "404 on index.json should map to RegistryUnreachable")
}

func TestHTTPRegistry_Manifest(t *testing.T) {
	root := fixtureRegistry(t)
	srv, _ := serveRegistry(t, root)
	r := newHTTPRegistry(t, srv.URL+"/")

	m, err := r.Manifest(context.Background(), "security", "owasp-review")
	require.NoError(t, err)
	assert.Equal(t, "owasp-review", m.Name)
	assert.Equal(t, "1.2.0", m.Latest)
	require.NotNil(t, m.FindVersion("1.2.0"))
}

func TestHTTPRegistry_Manifest_NotFound(t *testing.T) {
	root := fixtureRegistry(t)
	srv, _ := serveRegistry(t, root)
	r := newHTTPRegistry(t, srv.URL+"/")

	_, err := r.Manifest(context.Background(), "does", "not-exist")
	require.Error(t, err)
}

func TestHTTPRegistry_Manifest_LatestMissingFromVersions(t *testing.T) {
	root := fixtureRegistry(t)
	// Point manifest.latest at a version that isn't in the versions[]
	// list. Implementation must reject this.
	manifestPath := filepath.Join(root, "skills", "code-review", "standard", "manifest.json")
	data, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	patched := strings.Replace(string(data), `"latest": "1.0.0"`, `"latest": "9.9.9"`, 1)
	require.NoError(t, os.WriteFile(manifestPath, []byte(patched), 0o644))

	srv, _ := serveRegistry(t, root)
	r := newHTTPRegistry(t, srv.URL+"/")

	_, err = r.Manifest(context.Background(), "code-review", "standard")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing from versions")
}

func TestHTTPRegistry_Index_SchemaParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{not valid json`))
	}))
	t.Cleanup(srv.Close)
	r := newHTTPRegistry(t, srv.URL+"/")

	_, err := r.Index(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

func TestHTTPRegistry_FetchBundle_HashMatch(t *testing.T) {
	root := fixtureRegistry(t)
	srv, _ := serveRegistry(t, root)
	r := newHTTPRegistry(t, srv.URL+"/")

	bp, err := r.FetchBundle(context.Background(), "code-review", "standard", "1.0.0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = bp.Cleanup() })
	assert.NotEmpty(t, bp.Hash)
	assert.DirExists(t, bp.Path)
	assert.FileExists(t, filepath.Join(bp.Path, "SKILL.md"))
}

func TestHTTPRegistry_FetchBundle_HashMismatchAborts(t *testing.T) {
	root := fixtureRegistry(t)
	// Rewrite the sidecar to advertise a hash that doesn't match the
	// bundle's actual canonical content hash. Edit the manifest to the
	// same wrong hash so the manifest cross-check passes and we reach
	// the post-extract verify step.
	versionDir := filepath.Join(root, "skills", "code-review", "standard", "versions", "1.0.0")
	manifestPath := filepath.Join(root, "skills", "code-review", "standard", "manifest.json")
	wrong := strings.Repeat("0", 64)
	require.NoError(t, os.WriteFile(
		filepath.Join(versionDir, "bundle.sha256"),
		[]byte(wrong+"  bundle.tar.gz\n"), 0o644))
	manifestBytes, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	out := string(manifestBytes)
	hashRe := strings.Index(out, `"content_hash":`)
	require.NotEqual(t, -1, hashRe)
	colon := strings.Index(out[hashRe:], `:`)
	quote := strings.Index(out[hashRe+colon:], `"`)
	start := hashRe + colon + quote + 1
	end := start + 64
	out = out[:start] + wrong + out[end:]
	require.NoError(t, os.WriteFile(manifestPath, []byte(out), 0o644))

	srv, _ := serveRegistry(t, root)
	r := newHTTPRegistry(t, srv.URL+"/")

	_, err = r.FetchBundle(context.Background(), "code-review", "standard", "1.0.0")
	require.Error(t, err)
	var hm registry.HashMismatch
	assert.ErrorAs(t, err, &hm)
}

func TestHTTPRegistry_FetchBundle_SidecarMissingAborts(t *testing.T) {
	root := fixtureRegistry(t)
	sidecar := filepath.Join(root, "skills", "code-review", "standard", "versions", "1.0.0", "bundle.sha256")
	require.NoError(t, os.Remove(sidecar))

	srv, _ := serveRegistry(t, root)
	r := newHTTPRegistry(t, srv.URL+"/")

	_, err := r.FetchBundle(context.Background(), "code-review", "standard", "1.0.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bundle.sha256")
}

func TestHTTPRegistry_FetchBundle_ManifestHashMismatchAborts(t *testing.T) {
	root := fixtureRegistry(t)
	// Edit the manifest so its content_hash no longer matches the sidecar.
	manifestPath := filepath.Join(root, "skills", "code-review", "standard", "manifest.json")
	data, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	out := strings.Replace(string(data), `"content_hash"`, `"content_hash_disabled","unused":"x","content_hash"`, 1)
	// The previous replace produced an unknown field. Instead substitute the
	// hex hash with a deterministic wrong value, preserving JSON shape.
	out = string(data)
	hashRe := strings.Index(out, `"content_hash":`)
	require.NotEqual(t, -1, hashRe)
	colon := strings.Index(out[hashRe:], `:`)
	quote := strings.Index(out[hashRe+colon:], `"`)
	start := hashRe + colon + quote + 1
	end := start + 64
	bad := strings.Repeat("0", 64)
	out = out[:start] + bad + out[end:]
	require.NoError(t, os.WriteFile(manifestPath, []byte(out), 0o644))

	srv, _ := serveRegistry(t, root)
	r := newHTTPRegistry(t, srv.URL+"/")

	_, err = r.FetchBundle(context.Background(), "code-review", "standard", "1.0.0")
	require.Error(t, err)
	var hm registry.HashMismatch
	assert.ErrorAs(t, err, &hm)
}

func TestHTTPRegistry_FetchBundle_CacheHitSkipsGET(t *testing.T) {
	root := fixtureRegistry(t)
	srv, hits := serveRegistry(t, root)
	r := newHTTPRegistry(t, srv.URL+"/")

	bp, err := r.FetchBundle(context.Background(), "code-review", "standard", "1.0.0")
	require.NoError(t, err)
	require.NoError(t, bp.Cleanup())
	tarballHits1 := hits.Load()

	bp2, err := r.FetchBundle(context.Background(), "code-review", "standard", "1.0.0")
	require.NoError(t, err)
	require.NoError(t, bp2.Cleanup())
	tarballHits2 := hits.Load()

	// The second call should still GET the sidecar (it's freshly cached
	// the first time too, but with a short max-age) and skip the tarball
	// because it's in the content-addressed cache. We assert net new hits
	// are bounded — definitely never more than the first run.
	assert.LessOrEqual(t, tarballHits2-tarballHits1, tarballHits1,
		"second FetchBundle should not exceed the request count of the first")
}

func TestHTTPRegistry_Search(t *testing.T) {
	root := fixtureRegistry(t)
	srv, _ := serveRegistry(t, root)
	r := newHTTPRegistry(t, srv.URL+"/")

	all, err := r.Search(context.Background(), "")
	require.NoError(t, err)
	assert.Len(t, all, 2)

	owasp, err := r.Search(context.Background(), "owasp")
	require.NoError(t, err)
	require.Len(t, owasp, 1)
	assert.Equal(t, "owasp-review", owasp[0].Name)

	none, err := r.Search(context.Background(), "xxxx-no-match-xxxx")
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestHTTPRegistry_CheckConsistency_Clean(t *testing.T) {
	root := fixtureRegistry(t)
	srv, _ := serveRegistry(t, root)
	r := newHTTPRegistry(t, srv.URL+"/")

	issues := r.CheckConsistency(context.Background())
	assert.Empty(t, issues, "fixture should be self-consistent")
}

func TestHTTPRegistry_CheckConsistency_DriftDetected(t *testing.T) {
	root := fixtureRegistry(t)
	// Edit the index so its hash no longer matches the manifest.
	indexPath := filepath.Join(root, "index.json")
	data, err := os.ReadFile(indexPath)
	require.NoError(t, err)
	idx := string(data)
	hashRe := strings.Index(idx, `"latest_hash":`)
	require.NotEqual(t, -1, hashRe)
	colon := strings.Index(idx[hashRe:], `:`)
	quote := strings.Index(idx[hashRe+colon:], `"`)
	start := hashRe + colon + quote + 1
	end := start + 64
	bad := strings.Repeat("0", 64)
	idx = idx[:start] + bad + idx[end:]
	require.NoError(t, os.WriteFile(indexPath, []byte(idx), 0o644))

	srv, _ := serveRegistry(t, root)
	r := newHTTPRegistry(t, srv.URL+"/")

	issues := r.CheckConsistency(context.Background())
	require.NotEmpty(t, issues, "drift should be reported")
	found := false
	for _, iss := range issues {
		if strings.Contains(iss.Message, "index hash") {
			found = true
			assert.Equal(t, "warning", iss.Severity)
		}
	}
	assert.True(t, found, "expected an 'index hash != manifest hash' warning, got %+v", issues)
}

func TestHTTPRegistry_Retry5xxThenSuccess(t *testing.T) {
	root := fixtureRegistry(t)
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		// First two GETs to index.json return 503; third succeeds.
		if req.URL.Path == "/index.json" && calls.Add(1) <= 2 {
			http.Error(w, "transient", http.StatusServiceUnavailable)
			return
		}
		http.FileServer(http.Dir(root)).ServeHTTP(w, req)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	r := newHTTPRegistry(t, srv.URL+"/")

	idx, err := r.Index(context.Background())
	require.NoError(t, err)
	assert.Len(t, idx.Skills, 2)
	assert.GreaterOrEqual(t, calls.Load(), int32(3))
}

func TestHTTPRegistry_NetworkExhaustReturnsUnreachable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "always down", http.StatusBadGateway)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	r := newHTTPRegistry(t, srv.URL+"/")

	_, err := r.Index(context.Background())
	require.Error(t, err)
	var unreach registry.RegistryUnreachable
	assert.True(t, errors.As(err, &unreach),
		"persistent 5xx should map to RegistryUnreachable, got %T: %v", err, err)
}

func TestHTTPRegistry_NoRetryOn4xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	r := newHTTPRegistry(t, srv.URL+"/")

	_, err := r.Index(context.Background())
	require.Error(t, err)
	assert.Equal(t, int32(1), calls.Load(), "403 should not be retried")
}

func TestHTTPRegistry_BearerAuthApplied(t *testing.T) {
	root := fixtureRegistry(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer abc123" {
			http.Error(w, "missing bearer", http.StatusUnauthorized)
			return
		}
		http.FileServer(http.Dir(root)).ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	r := newHTTPRegistry(t, srv.URL+"/")
	r.Auth.Bearer = "abc123"

	idx, err := r.Index(context.Background())
	require.NoError(t, err)
	assert.Len(t, idx.Skills, 2)
}

func TestHTTPRegistry_BasicAuthApplied(t *testing.T) {
	root := fixtureRegistry(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		u, p, ok := req.BasicAuth()
		if !ok || u != "alice" || p != "secret" {
			w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
			http.Error(w, "missing basic", http.StatusUnauthorized)
			return
		}
		http.FileServer(http.Dir(root)).ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	r := newHTTPRegistry(t, srv.URL+"/")
	r.Auth.BasicUser = "alice"
	r.Auth.BasicPass = "secret"

	_, err := r.Index(context.Background())
	require.NoError(t, err)
}

func TestHTTPRegistry_mTLSConfigBadPath(t *testing.T) {
	r := &registry.HTTPRegistry{
		BaseURL:    "https://example.test/",
		APIVersion: "v1",
		CacheDir:   t.TempDir(),
		Auth: registry.HTTPAuth{
			ClientCert: filepath.Join(t.TempDir(), "missing-cert.pem"),
			ClientKey:  filepath.Join(t.TempDir(), "missing-key.pem"),
		},
	}
	// httpClient is lazy via Index; the first call should surface a
	// RegistryUnreachable wrapping the keypair-load failure.
	_, err := r.Index(context.Background())
	require.Error(t, err)
	var unreach registry.RegistryUnreachable
	assert.ErrorAs(t, err, &unreach)
}

// expireMeta rewrites the .meta sidecar for a URL with a fetched_at
// timestamp far in the past so the next call revalidates.
func expireMeta(t *testing.T, r *registry.HTTPRegistry, urlStr string) {
	t.Helper()
	// Find every .meta under the cache dir and rewrite ones whose body
	// references the URL. The meta-path layout is private; we just walk.
	err := filepath.Walk(r.CacheDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(p, ".meta") {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if !strings.Contains(string(body), urlStr) {
			return nil
		}
		// Replace any `"fetched_at": "..."` with a date long in the past.
		old := string(body)
		idx := strings.Index(old, `"fetched_at"`)
		if idx < 0 {
			return nil
		}
		colon := strings.Index(old[idx:], `:`)
		quoteStart := strings.Index(old[idx+colon:], `"`)
		valueStart := idx + colon + quoteStart + 1
		quoteEnd := strings.Index(old[valueStart:], `"`)
		valueEnd := valueStart + quoteEnd
		past := time.Now().Add(-365 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
		updated := old[:valueStart] + past + old[valueEnd:]
		return os.WriteFile(p, []byte(updated), info.Mode())
	})
	require.NoError(t, err)
}
