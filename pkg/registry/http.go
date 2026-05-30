package registry

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/forge/fdh/pkg/bundle"
)

// HTTPRegistry is a Registry implementation backed by a static-file HTTP
// server speaking the wire protocol documented in design.md of the
// implement-http-registry-consumer change (and ultimately the
// hub-http-registry spec, once it lands). Reads happen against the live
// server; responses are content-addressed in a local on-disk cache so
// re-runs of the CLI never re-fetch immutable bundles and only revalidate
// index.json / manifest.json after a small TTL.
//
// HTTPRegistry shares package-level types (Index, Manifest, BundlePath,
// ConsistencyIssue, RegistryUnreachable, HashMismatch) with GitRegistry to
// keep the dispatcher / call sites transport-agnostic.
type HTTPRegistry struct {
	// BaseURL is the absolute URL the registry tree is rooted at.
	// MUST end with "/". Example: "https://fdh.askenaz.dev/v1/".
	BaseURL string

	// APIVersion is the protocol version surfaced by the wire URLs. The
	// canonical layout assumes a "/v1/" path segment in BaseURL itself;
	// APIVersion is kept as a separate field for diagnostic output and
	// future v2 negotiation.
	APIVersion string

	// CacheDir is the absolute path to the on-disk HTTP cache. The
	// cache is content-addressed (objects/<sha[:2]>/<sha>.bin) with a
	// per-URL sidecar (index/<host>/<path>.meta) holding ETag,
	// fetched-at, and cache-control metadata.
	CacheDir string

	// HTTPClient is the underlying client. If nil, a default client is
	// constructed lazily via httpClient(); the default applies a 30s
	// per-request timeout and the configured Auth.
	HTTPClient *http.Client

	// Auth carries the optional authentication material (bearer / basic
	// / mTLS). Zero value means "no auth".
	Auth HTTPAuth

	// Logger receives one-line operational messages. nil discards them.
	Logger func(line string)

	clientOnce sync.Once
	clientErr  error
}

// HTTPAuth bundles the supported authentication options.
//
//   - If Bearer != "" the client adds "Authorization: Bearer <token>".
//   - If BasicUser != "" the client adds basic auth headers.
//   - If ClientCert and ClientKey are both != "" the client loads the
//     PEM-encoded keypair and presents it for mTLS handshakes.
//
// All fields zero ⇒ no authentication.
type HTTPAuth struct {
	Bearer     string
	BasicUser  string
	BasicPass  string
	ClientCert string
	ClientKey  string
}

// cacheMeta is the JSON sidecar persisted alongside cached objects. One
// meta record per URL; the SHA points at the content-addressed blob in
// objects/.
type cacheMeta struct {
	SHA256      string    `json:"sha256"`
	ETag        string    `json:"etag,omitempty"`
	FetchedAt   time.Time `json:"fetched_at"`
	MaxAge      int64     `json:"max_age_seconds"`
	ContentType string    `json:"content_type,omitempty"`
	Immutable   bool      `json:"immutable,omitempty"`
	OriginalURL string    `json:"url"`
}

// IsStale reports whether the cached resource exceeded its max-age. An
// immutable resource is never stale.
func (m cacheMeta) IsStale(now time.Time) bool {
	if m.Immutable {
		return false
	}
	if m.MaxAge <= 0 {
		// No max-age advertised. Treat as immediately stale so the next
		// call revalidates with If-None-Match.
		return true
	}
	return now.Sub(m.FetchedAt) > time.Duration(m.MaxAge)*time.Second
}

// log writes a one-line message via Logger if set.
func (r *HTTPRegistry) log(format string, args ...any) {
	if r.Logger == nil {
		return
	}
	r.Logger(fmt.Sprintf(format, args...))
}

// --- Registry interface methods ---

// Source returns a human-readable description of the registry transport.
// Format: "http:<base>?api=<version>" — consumed by `fdh doctor`.
func (r *HTTPRegistry) Source() string {
	return fmt.Sprintf("http:%s?api=%s", r.BaseURL, r.APIVersion)
}

// Index fetches and returns <base>/index.json. The result is cached on
// disk and revalidated with If-None-Match once the TTL expires.
func (r *HTTPRegistry) Index(ctx context.Context) (Index, error) {
	u := r.BaseURL + "index.json"
	body, err := r.fetchCached(ctx, u)
	if err != nil {
		if isNotFound(err) {
			return Index{}, RegistryUnreachable{
				Detail: fmt.Sprintf("registry root not found at %s; check registry.url", u),
			}
		}
		return Index{}, err
	}
	var idx Index
	if err := unmarshalStrict(body, &idx); err != nil {
		return Index{}, fmt.Errorf("parse %s: %w", u, err)
	}
	idx.normalize()
	return idx, nil
}

// Manifest fetches and returns the per-skill manifest.
//
// Equivalent to ManifestByKind(ctx, "skill", namespace, name).
func (r *HTTPRegistry) Manifest(ctx context.Context, namespace, name string) (Manifest, error) {
	return r.ManifestByKind(ctx, "skill", namespace, name)
}

// ManifestByKind fetches the per-component manifest for the
// (kind, namespace, name) tuple. URL: <base>/<kind-plural>/<ns>/<name>/manifest.json.
func (r *HTTPRegistry) ManifestByKind(ctx context.Context, kind, namespace, name string) (Manifest, error) {
	plural := kindPlural(kind)
	if plural == "" {
		return Manifest{}, fmt.Errorf("unknown kind %q (want skill|rule|agent|hook)", kind)
	}
	u := r.BaseURL + path(plural, namespace, name, "manifest.json")
	body, err := r.fetchCached(ctx, u)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := unmarshalStrict(body, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse %s: %w", u, err)
	}
	if len(m.Versions) == 0 {
		return Manifest{}, fmt.Errorf("manifest %s/%s/%s has no versions", kind, namespace, name)
	}
	if m.FindVersion(m.Latest) == nil {
		return Manifest{}, fmt.Errorf("manifest %s/%s/%s latest=%s missing from versions[]", kind, namespace, name, m.Latest)
	}
	return m, nil
}

// kindPlural maps a kind to its URL plural segment.
func kindPlural(kind string) string {
	switch kind {
	case "skill":
		return "skills"
	case "rule":
		return "rules"
	case "agent":
		return "agents"
	case "hook":
		return "hooks"
	}
	return ""
}

// FetchBundle fetches and verifies the requested bundle. The bundle.sha256
// sidecar is fetched first to learn the expected canonical content hash;
// the tarball is then downloaded (or pulled from cache), extracted to a
// temp dir, and its canonical hash is verified against the sidecar.
//
// Hash semantics match GitRegistry: bundle.sha256 carries the canonical
// content hash of the extracted bundle directory (not the SHA of the
// tar.gz bytes). The HTTP cache is keyed by the SHA of the downloaded
// tarball bytes — a separate quantity that lets us short-circuit re-GET
// on byte-identical responses without re-extracting.
func (r *HTTPRegistry) FetchBundle(ctx context.Context, namespace, name, version string) (BundlePath, error) {
	return r.FetchBundleByKind(ctx, "skill", namespace, name, version)
}

// FetchBundleByKind extends FetchBundle with explicit kind routing.
func (r *HTTPRegistry) FetchBundleByKind(ctx context.Context, kind, namespace, name, version string) (BundlePath, error) {
	plural := kindPlural(kind)
	if plural == "" {
		return BundlePath{}, fmt.Errorf("unknown kind %q (want skill|rule|agent|hook)", kind)
	}
	base := r.BaseURL + path(plural, namespace, name, "versions", version) + "/"
	sumURL := base + "bundle.sha256"
	tarURL := base + "bundle.tar.gz"

	sumBytes, err := r.fetchCached(ctx, sumURL)
	if err != nil {
		return BundlePath{}, fmt.Errorf("fetch bundle.sha256: %w", err)
	}
	expectedSHA, err := parseSidecar(sumBytes)
	if err != nil {
		return BundlePath{}, fmt.Errorf("parse bundle.sha256: %w", err)
	}

	// Cross-check the sidecar against the manifest entry when one is
	// available. A mismatch is fatal — the sidecar and the manifest both
	// describe the canonical content hash, so they must agree.
	if m, mErr := r.ManifestByKind(ctx, kind, namespace, name); mErr == nil {
		if v := m.FindVersion(version); v != nil && v.ContentHash != "" && v.ContentHash != expectedSHA {
			return BundlePath{}, HashMismatch{Expected: v.ContentHash, Got: expectedSHA}
		}
	}

	// Download the tarball through the cached path so byte-identical
	// re-fetches skip the network.
	tarBytes, err := r.fetchCached(ctx, tarURL)
	if err != nil {
		return BundlePath{}, fmt.Errorf("fetch bundle.tar.gz: %w", err)
	}

	// Extract into a temp dir and compute the canonical hash. We don't
	// commit anything to the install target until the hash matches.
	tmp, err := os.MkdirTemp("", "fdh-http-bundle-*")
	if err != nil {
		return BundlePath{}, fmt.Errorf("mkdtemp: %w", err)
	}
	cleanupTmp := func() error { return os.RemoveAll(tmp) }

	if err := writeFile(filepath.Join(tmp, "bundle.tar.gz"), tarBytes); err != nil {
		_ = cleanupTmp()
		return BundlePath{}, err
	}
	if err := extractTarGz(filepath.Join(tmp, "bundle.tar.gz"), tmp); err != nil {
		_ = cleanupTmp()
		return BundlePath{}, fmt.Errorf("extract bundle: %w", err)
	}
	extracted, err := locateBundleDir(tmp)
	if err != nil {
		_ = cleanupTmp()
		return BundlePath{}, err
	}
	// Rename the extracted dir to the skill's name so bundle.Validate
	// sees a directory whose name matches the SKILL.md frontmatter —
	// same invariant GitRegistry enforces.
	renamed := filepath.Join(tmp, name)
	if extracted != renamed {
		if err := os.Rename(extracted, renamed); err != nil {
			_ = cleanupTmp()
			return BundlePath{}, fmt.Errorf("rename extracted bundle: %w", err)
		}
		extracted = renamed
	}

	loaded, err := bundle.Load(extracted)
	if err != nil {
		_ = cleanupTmp()
		return BundlePath{}, fmt.Errorf("load extracted bundle: %w", err)
	}
	got, err := loaded.Hash()
	if err != nil {
		_ = cleanupTmp()
		return BundlePath{}, fmt.Errorf("hash extracted bundle: %w", err)
	}
	if got != expectedSHA {
		_ = cleanupTmp()
		return BundlePath{}, HashMismatch{Expected: expectedSHA, Got: got}
	}

	return BundlePath{
		Path:    extracted,
		Hash:    expectedSHA,
		cleanup: cleanupTmp,
	}, nil
}

// Search reuses Index() and filters in memory using the shared matchQuery
// helper. Empty query returns every entry.
func (r *HTTPRegistry) Search(ctx context.Context, query string) ([]SkillSummary, error) {
	idx, err := r.Index(ctx)
	if err != nil {
		return nil, err
	}
	var out []SkillSummary
	for _, e := range idx.Skills {
		blob := strings.Join([]string{e.Namespace, e.Name, e.Description, strings.Join(e.Tags, " ")}, " ")
		if !matchQuery(blob, query) {
			continue
		}
		out = append(out, e.toSummary())
	}
	return out, nil
}

// CheckConsistency cross-references the index with each manifest. It only
// fetches metadata (index + manifest), never bundles.
func (r *HTTPRegistry) CheckConsistency(ctx context.Context) []ConsistencyIssue {
	var issues []ConsistencyIssue
	idx, err := r.Index(ctx)
	if err != nil {
		return []ConsistencyIssue{{Severity: "error", Message: err.Error()}}
	}
	for _, e := range idx.Skills {
		m, err := r.Manifest(ctx, e.Namespace, e.Name)
		if err != nil {
			issues = append(issues, ConsistencyIssue{
				Skill:    e.Namespace + "/" + e.Name,
				Severity: "error",
				Message:  fmt.Sprintf("manifest unreadable: %v", err),
			})
			continue
		}
		if m.Latest != e.LatestVersion {
			issues = append(issues, ConsistencyIssue{
				Skill:    e.Namespace + "/" + e.Name,
				Severity: "warning",
				Message:  fmt.Sprintf("index latest=%s but manifest latest=%s", e.LatestVersion, m.Latest),
			})
		}
		if v := m.FindVersion(m.Latest); v != nil {
			if v.ContentHash != e.LatestHash {
				issues = append(issues, ConsistencyIssue{
					Skill:    e.Namespace + "/" + e.Name,
					Severity: "warning",
					Message:  fmt.Sprintf("index hash %s != manifest hash %s for latest=%s", e.LatestHash, v.ContentHash, m.Latest),
				})
			}
		}
	}
	return issues
}

// --- HTTP client construction ---

// httpClient returns the configured http.Client, building it lazily on
// first use. Failures (e.g., loading an mTLS keypair from a bad path)
// surface as RegistryUnreachable so the CLI maps them to exit 3.
func (r *HTTPRegistry) httpClient() (*http.Client, error) {
	if r.HTTPClient != nil {
		return r.HTTPClient, nil
	}
	r.clientOnce.Do(func() {
		tr := &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          16,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}

		if r.Auth.ClientCert != "" || r.Auth.ClientKey != "" {
			if r.Auth.ClientCert == "" || r.Auth.ClientKey == "" {
				r.clientErr = RegistryUnreachable{
					Detail: "mTLS requires both registry.http.auth.client_cert and client_key",
				}
				return
			}
			cert, err := tls.LoadX509KeyPair(r.Auth.ClientCert, r.Auth.ClientKey)
			if err != nil {
				r.clientErr = RegistryUnreachable{
					Detail: fmt.Sprintf("load mTLS keypair: %v", err),
				}
				return
			}
			tr.TLSClientConfig = &tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
			}
		}

		r.HTTPClient = &http.Client{
			Transport: &authRoundTripper{base: tr, auth: r.Auth},
			Timeout:   30 * time.Second,
		}
	})
	return r.HTTPClient, r.clientErr
}

// authRoundTripper injects Authorization headers before delegating to the
// wrapped transport. Centralized here so retries, redirects, and future
// transport wrappers all benefit.
type authRoundTripper struct {
	base http.RoundTripper
	auth HTTPAuth
}

func (a *authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if a.auth.Bearer != "" {
		req.Header.Set("Authorization", "Bearer "+a.auth.Bearer)
	} else if a.auth.BasicUser != "" {
		req.SetBasicAuth(a.auth.BasicUser, a.auth.BasicPass)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "fdh-http-registry/1")
	}
	return a.base.RoundTrip(req)
}

// doRequest issues an HTTP request with exponential backoff over network
// errors and 5xx responses. 4xx responses bubble up immediately. The
// returned response body is the caller's responsibility to close.
func (r *HTTPRegistry) doRequest(ctx context.Context, method, urlStr, ifNoneMatch string) (*http.Response, error) {
	client, err := r.httpClient()
	if err != nil {
		return nil, err
	}

	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, urlStr, nil)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		req.Header.Set("Accept", "*/*")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			r.log("attempt %d: %s %s: %v", attempt+1, method, urlStr, err)
			if attempt == maxAttempts-1 {
				break
			}
			sleepBackoff(ctx, attempt)
			continue
		}
		if resp.StatusCode >= 500 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			r.log("attempt %d: %s %s: HTTP %d", attempt+1, method, urlStr, resp.StatusCode)
			if attempt == maxAttempts-1 {
				break
			}
			sleepBackoff(ctx, attempt)
			continue
		}
		return resp, nil
	}
	return nil, RegistryUnreachable{
		Detail: fmt.Sprintf("%s %s: %v", method, urlStr, lastErr),
	}
}

// sleepBackoff sleeps for the canonical backoff for the given attempt
// (0-based), or returns early if ctx is canceled. Sleep durations are
// 100ms / 200ms / 400ms ± 25% jitter.
func sleepBackoff(ctx context.Context, attempt int) {
	base := 100 * time.Millisecond * time.Duration(1<<attempt)
	jitter := time.Duration(rand.Int63n(int64(base/2))) - base/4
	delay := base + jitter
	select {
	case <-time.After(delay):
	case <-ctx.Done():
	}
}

// --- Cache layer ---

// fetchCached returns the body of urlStr, using the on-disk cache when
// possible. For immutable resources (Cache-Control: immutable) a cache
// hit is unconditional. For mutable resources the cache is revalidated
// with If-None-Match once max-age expires.
func (r *HTTPRegistry) fetchCached(ctx context.Context, urlStr string) ([]byte, error) {
	meta, metaErr := r.readMeta(urlStr)
	now := time.Now()

	// Cache hit on immutable or unexpired resource — return the cached
	// object without any HTTP I/O.
	if metaErr == nil && !meta.IsStale(now) {
		if body, err := os.ReadFile(r.objectPath(meta.SHA256)); err == nil {
			r.log("cache hit (fresh): %s", urlStr)
			return body, nil
		}
	}

	ifNoneMatch := ""
	if metaErr == nil {
		ifNoneMatch = meta.ETag
	}

	resp, err := r.doRequest(ctx, http.MethodGet, urlStr, ifNoneMatch)
	if err != nil {
		// Network or 5xx exhaustion. If we have a cached copy, fall back
		// to it (read-cached on failure is consistent with GitRegistry's
		// "use cached data" log path).
		if metaErr == nil {
			if body, rerr := os.ReadFile(r.objectPath(meta.SHA256)); rerr == nil {
				r.log("network failure for %s; using cached copy: %v", urlStr, err)
				return body, nil
			}
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified && metaErr == nil {
		// 304 — refresh fetched_at and reuse the cached object.
		meta.FetchedAt = now
		meta.MaxAge, meta.Immutable = parseCacheControl(resp.Header.Get("Cache-Control"))
		if etag := resp.Header.Get("ETag"); etag != "" {
			meta.ETag = etag
		}
		if err := r.writeMeta(urlStr, meta); err != nil {
			r.log("refresh meta failed for %s: %v (continuing)", urlStr, err)
		}
		body, err := os.ReadFile(r.objectPath(meta.SHA256))
		if err != nil {
			return nil, fmt.Errorf("read cached object after 304: %w", err)
		}
		r.log("cache hit (304): %s", urlStr)
		return body, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, notFoundError(urlStr)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: unexpected status %d", urlStr, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	sha := sha256Hex(body)
	if err := r.writeObject(sha, body); err != nil {
		return nil, fmt.Errorf("cache object: %w", err)
	}
	maxAge, immutable := parseCacheControl(resp.Header.Get("Cache-Control"))
	newMeta := cacheMeta{
		SHA256:      sha,
		ETag:        resp.Header.Get("ETag"),
		FetchedAt:   now,
		MaxAge:      maxAge,
		Immutable:   immutable,
		ContentType: resp.Header.Get("Content-Type"),
		OriginalURL: urlStr,
	}
	if err := r.writeMeta(urlStr, newMeta); err != nil {
		r.log("write meta failed for %s: %v (continuing)", urlStr, err)
	}
	return body, nil
}

func (r *HTTPRegistry) objectPath(sha string) string {
	if len(sha) < 2 {
		return ""
	}
	return filepath.Join(r.CacheDir, "objects", sha[:2], sha+".bin")
}

func (r *HTTPRegistry) metaPath(urlStr string) string {
	host, p, err := splitURLPath(urlStr)
	if err != nil {
		// Fallback to a sanitized form so a malformed URL still produces
		// a deterministic path; lookups still match because we use the
		// same function for read and write.
		host, p = "unknown", sanitizeURLPath(urlStr)
	}
	return filepath.Join(r.CacheDir, "index", host, p+".meta")
}

func (r *HTTPRegistry) writeObject(sha string, body []byte) error {
	p := r.objectPath(sha)
	if p == "" {
		return fmt.Errorf("invalid sha %q", sha)
	}
	if _, err := os.Stat(p); err == nil {
		// Already present; nothing to do.
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), "obj-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func (r *HTTPRegistry) writeMeta(urlStr string, m cacheMeta) error {
	p := r.metaPath(urlStr)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func (r *HTTPRegistry) readMeta(urlStr string) (cacheMeta, error) {
	p := r.metaPath(urlStr)
	data, err := os.ReadFile(p)
	if err != nil {
		return cacheMeta{}, err
	}
	var m cacheMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return cacheMeta{}, err
	}
	return m, nil
}

// --- helpers ---

func path(segments ...string) string {
	return strings.Join(segments, "/")
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func writeFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

// parseSidecar reads a bundle.sha256 file. The canonical format produced
// by publish is "<sha>  bundle.tar.gz\n"; we accept any leading hex token
// to be liberal in what we accept.
func parseSidecar(body []byte) (string, error) {
	fields := strings.Fields(string(body))
	if len(fields) == 0 {
		return "", errors.New("bundle.sha256 is empty")
	}
	sha := strings.ToLower(fields[0])
	if len(sha) != 64 {
		return "", fmt.Errorf("bundle.sha256: token %q is not a 64-hex SHA", sha)
	}
	for _, c := range sha {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", fmt.Errorf("bundle.sha256: non-hex char %q", c)
		}
	}
	return sha, nil
}

// parseCacheControl returns (max-age-seconds, immutable). Unrecognized
// directives are ignored.
func parseCacheControl(h string) (int64, bool) {
	if h == "" {
		return 0, false
	}
	var maxAge int64
	immutable := false
	for _, part := range strings.Split(h, ",") {
		p := strings.TrimSpace(strings.ToLower(part))
		switch {
		case p == "immutable":
			immutable = true
		case strings.HasPrefix(p, "max-age="):
			if v, err := strconv.ParseInt(strings.TrimPrefix(p, "max-age="), 10, 64); err == nil {
				maxAge = v
			}
		}
	}
	return maxAge, immutable
}

// splitURLPath returns ("host", "path") for a parsed URL, with both
// sanitized so they can be used as filesystem path segments on Windows
// (which forbids ':' anywhere — including in "host:port"). The path
// strips its leading "/" and folds any query into the path so two
// requests with different queries occupy distinct meta files.
func splitURLPath(urlStr string) (string, string, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return "", "", err
	}
	host := sanitizeURLPath(u.Host)
	p := strings.TrimPrefix(u.Path, "/")
	if u.RawQuery != "" {
		p += "__q__" + sanitizeURLPath(u.RawQuery)
	}
	return host, p, nil
}

// sanitizeURLPath removes filesystem-hostile characters so a URL fragment
// can be used as a path segment on Windows.
func sanitizeURLPath(s string) string {
	bad := []string{":", "?", "*", "<", ">", "|", "\""}
	for _, b := range bad {
		s = strings.ReplaceAll(s, b, "_")
	}
	return s
}

// notFoundError wraps a 404 with a sentinel that isNotFound recognizes.
func notFoundError(urlStr string) error {
	return &httpNotFound{url: urlStr}
}

type httpNotFound struct {
	url string
}

func (e *httpNotFound) Error() string {
	return fmt.Sprintf("GET %s: 404 Not Found", e.url)
}

func isNotFound(err error) bool {
	var nf *httpNotFound
	return errors.As(err, &nf)
}

// Compile-time assertion that *HTTPRegistry satisfies the Registry interface.
var _ Registry = (*HTTPRegistry)(nil)
