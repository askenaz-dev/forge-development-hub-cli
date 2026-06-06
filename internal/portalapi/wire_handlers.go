package portalapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/forge/fdh/pkg/bundle"
)

// Wire-protocol constants. These are sentinels until the hub formalizes
// per-version metadata (registry.yaml v3+); pinning to fixed values keeps
// every response's ETag stable across portal pod respins. scan_status is the
// exception — it is now the real fdh-scan verdict (capability
// portal-scan-status), not a sentinel.
const (
	wireVersion     = "latest"
	wirePublishedAt = "1970-01-01T00:00:00Z"
	wirePublishedBy = "hub"
)

// deriveNamespace maps a component's owner_team to its wire namespace per the
// hub-http-registry canonical rule: lowercase, "_"→"-", drop chars outside
// [a-z0-9-], trim leading/trailing "-". Empty input falls back to "unknown".
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

// wirePublishedAtTime returns wirePublishedAt parsed as a UTC time.Time. The
// same value is used both as the manifest's published_at field and as the
// ModTime override for entries inside the deterministic tarball, so the
// content_hash and the bytes-SHA stay in sync across requests.
func wirePublishedAtTime() time.Time {
	t, _ := time.Parse(time.RFC3339, wirePublishedAt)
	return t.UTC()
}

// hubCatalog mirrors the v2 schema of <hub_path>/hub/registry.yaml.
type hubCatalog struct {
	SchemaVersion int            `yaml:"schema_version"`
	HubVersion    string         `yaml:"hub_version"`
	Components    []hubComponent `yaml:"components"`
}

type hubComponent struct {
	Name        string   `yaml:"name"`
	Kind        string   `yaml:"kind"`
	Description string   `yaml:"description"`
	OwnerTeam   string   `yaml:"owner_team"`
	Tags        []string `yaml:"tags,omitempty"`
	Default     bool     `yaml:"default,omitempty"`
	MinFDHVer   string   `yaml:"min_fdh_version,omitempty"`
	AgentsSup   []string `yaml:"agents_supported,omitempty"`
	Path        string   `yaml:"path"`
}

func (c *hubCatalog) findComponent(kind, name string) *hubComponent {
	for i := range c.Components {
		if c.Components[i].Kind == kind && c.Components[i].Name == name {
			return &c.Components[i]
		}
	}
	return nil
}

// loadHubCatalog reads and parses <hubPath>/hub/registry.yaml. The
// underlying os.ReadFile error is preserved so the caller can branch on
// fs.ErrNotExist to emit 503.
func loadHubCatalog(hubPath string) (*hubCatalog, error) {
	p := filepath.Join(hubPath, "hub", "registry.yaml")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var c hubCatalog
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return &c, nil
}

// pluralToKind maps URL plural segments to the catalog's singular kind. The
// inverse direction (kind → URL segment) is not needed because handlers
// always receive the plural form from the router.
func pluralToKind(plural string) (string, bool) {
	switch plural {
	case "skills":
		return "skill", true
	case "rules":
		return "rule", true
	case "agents":
		return "agent", true
	case "hooks":
		return "hook", true
	default:
		return "", false
	}
}

// --- JSON shapes ---
//
// These structs are parallel to pkg/registry.Index / .Manifest / .Version
// to guarantee the consumer's strict-decode succeeds. Any divergence will
// surface as a test failure in pkg/registry's e2e tests.

type wireIndexEntry struct {
	Kind          string   `json:"kind"`
	Namespace     string   `json:"namespace"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	OwnerTeam     string   `json:"owner_team"`
	Tags          []string `json:"tags,omitempty"`
	LatestVersion string   `json:"latest_version"`
	LatestHash    string   `json:"latest_hash"`
	ScanStatus    string   `json:"scan_status"`
}

type wireIndex struct {
	SchemaVersion int              `json:"schema_version"`
	Registry      string           `json:"registry"`
	Components    []wireIndexEntry `json:"components"`
}

type wireVersionEntry struct {
	Version     string `json:"version"`
	ContentHash string `json:"content_hash"`
	PublishedAt string `json:"published_at"`
	PublishedBy string `json:"published_by"`
	ScanStatus  string `json:"scan_status"`
	Signature   string `json:"signature,omitempty"`
}

type wireManifest struct {
	SchemaVersion int                `json:"schema_version"`
	Namespace     string             `json:"namespace"`
	Name          string             `json:"name"`
	Description   string             `json:"description"`
	OwnerTeam     string             `json:"owner_team"`
	Tags          []string           `json:"tags,omitempty"`
	Latest        string             `json:"latest"`
	Versions      []wireVersionEntry `json:"versions"`
}

// --- Handlers ---

func (s *Server) handleWireIndex(w http.ResponseWriter, r *http.Request) {
	cat, err := loadHubCatalog(s.cfg.HubPath)
	if errors.Is(err, fs.ErrNotExist) {
		s.respondHubNotReady(w)
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "catalog_unreadable", err.Error())
		return
	}

	idx := wireIndex{
		SchemaVersion: 2,
		Registry:      "forge-development-hub",
		Components:    []wireIndexEntry{},
	}

	for _, comp := range cat.Components {
		// Only the four known kinds are served on the wire; an unknown kind
		// in the catalog is skipped (logged) rather than silently emitted.
		switch comp.Kind {
		case "skill", "rule", "agent", "hook":
		default:
			s.logger.Warn("wire index: skipping component with unknown kind",
				"name", comp.Name, "kind", comp.Kind)
			continue
		}
		bundleDir := filepath.Join(s.cfg.HubPath, filepath.FromSlash(comp.Path))
		vers, err := componentVersions(s.cfg.HubPath, bundleDir, comp.Path, comp.Kind, comp.Name)
		if err != nil || len(vers) == 0 {
			s.logger.Warn("wire index: skipping component, versions failed",
				"name", comp.Name, "kind", comp.Kind, "err", err)
			continue
		}
		latest := vers[0]
		idx.Components = append(idx.Components, wireIndexEntry{
			Kind:          comp.Kind,
			Namespace:     deriveNamespace(comp.OwnerTeam),
			Name:          comp.Name,
			Description:   comp.Description,
			OwnerTeam:     comp.OwnerTeam,
			Tags:          comp.Tags,
			LatestVersion: latest.Version,
			LatestHash:    latest.ContentHash,
			ScanStatus:    s.scanStatusFor(latest.ContentHash, bundleDir),
		})
	}

	sort.Slice(idx.Components, func(i, j int) bool {
		if idx.Components[i].Kind != idx.Components[j].Kind {
			return idx.Components[i].Kind < idx.Components[j].Kind
		}
		return idx.Components[i].Name < idx.Components[j].Name
	})

	body, err := json.Marshal(idx)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "encode_failed", err.Error())
		return
	}
	s.writeWireJSON(w, r, body, false)
}

func (s *Server) handleWireManifest(w http.ResponseWriter, r *http.Request) {
	kindURL := r.PathValue("kindPlural")
	namespace := r.PathValue("namespace")
	name := r.PathValue("name")

	comp, srcDir, ok := s.lookupComponent(w, r, kindURL, namespace, name)
	if !ok {
		return
	}

	vers, err := componentVersions(s.cfg.HubPath, srcDir, comp.Path, comp.Kind, comp.Name)
	if err != nil || len(vers) == 0 {
		s.writeError(w, http.StatusInternalServerError, "versions_failed", "could not resolve component versions")
		return
	}

	compStatus := s.componentScanStatus(comp, vers)
	latest := vers[0].Version
	entries := make([]wireVersionEntry, 0, len(vers))
	for _, v := range vers {
		entries = append(entries, wireVersionEntry{
			Version:     v.Version,
			ContentHash: v.ContentHash,
			PublishedAt: v.PublishedAt.UTC().Format(time.RFC3339),
			PublishedBy: wirePublishedBy,
			ScanStatus:  versionScanStatus(v, latest, compStatus),
			Signature:   v.Signature,
		})
	}

	m := wireManifest{
		SchemaVersion: 1,
		Namespace:     deriveNamespace(comp.OwnerTeam),
		Name:          comp.Name,
		Description:   comp.Description,
		OwnerTeam:     comp.OwnerTeam,
		Tags:          comp.Tags,
		Latest:        vers[0].Version,
		Versions:      entries,
	}

	body, err := json.Marshal(m)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "encode_failed", err.Error())
		return
	}
	s.writeWireJSON(w, r, body, false)
}

func (s *Server) handleWireBundleTarball(w http.ResponseWriter, r *http.Request) {
	kindURL := r.PathValue("kindPlural")
	namespace := r.PathValue("namespace")
	name := r.PathValue("name")
	version := r.PathValue("version")

	comp, srcDir, ok := s.lookupComponent(w, r, kindURL, namespace, name)
	if !ok {
		return
	}
	dir, cleanup, rv, found, err := resolveVersionSource(s.cfg.HubPath, srcDir, comp.Path, comp.Kind, comp.Name, version)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "version_resolve_failed", err.Error())
		return
	}
	if !found {
		s.respondWireNotFound(w, namespace, name, version)
		return
	}
	defer cleanup()

	// Lifecycle: yanked versions are removed from the wire protocol —
	// serve 410 Gone with a tiny advisory body, no caching.
	if rv.Status == "yanked" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusGone)
		_, _ = fmt.Fprintf(w, "version %s of %s/%s is yanked\n", version, namespace, name)
		return
	}

	data, sha, err := bundle.BuildDeterministicTarball(dir, wirePublishedAtTime())
	if err != nil {
		if strings.Contains(err.Error(), "unsupported_entry") {
			s.writeError(w, http.StatusInternalServerError, "unsupported_entry", err.Error())
			return
		}
		s.writeError(w, http.StatusInternalServerError, "tarball_build_failed", err.Error())
		return
	}

	etag := `"` + sha + `"`
	if r.Header.Get("If-None-Match") == etag {
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleWireBundleSidecar(w http.ResponseWriter, r *http.Request) {
	kindURL := r.PathValue("kindPlural")
	namespace := r.PathValue("namespace")
	name := r.PathValue("name")
	version := r.PathValue("version")

	comp, srcDir, ok := s.lookupComponent(w, r, kindURL, namespace, name)
	if !ok {
		return
	}
	_, cleanup, rv, found, err := resolveVersionSource(s.cfg.HubPath, srcDir, comp.Path, comp.Kind, comp.Name, version)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "version_resolve_failed", err.Error())
		return
	}
	if !found {
		s.respondWireNotFound(w, namespace, name, version)
		return
	}
	defer cleanup()

	body := fmt.Sprintf("%s  bundle.tar.gz\n", rv.ContentHash)
	sum := sha256.Sum256([]byte(body))
	etag := `"` + hex.EncodeToString(sum[:]) + `"`

	if r.Header.Get("If-None-Match") == etag {
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// lookupComponent validates the URL segments against the catalog and returns
// the resolved component plus its on-disk source path (the working-tree dir).
// Version resolution + per-version hashing is handled separately by
// resolveVersionSource / componentVersions. On any failure the appropriate HTTP
// response is emitted and ok is false.
func (s *Server) lookupComponent(w http.ResponseWriter, r *http.Request,
	kindURL, namespace, name string,
) (*hubComponent, string, bool) {

	kind, ok := pluralToKind(kindURL)
	if !ok {
		s.respondWireNotFound(w, namespace, name, "")
		return nil, "", false
	}

	cat, err := loadHubCatalog(s.cfg.HubPath)
	if errors.Is(err, fs.ErrNotExist) {
		s.respondHubNotReady(w)
		return nil, "", false
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "catalog_unreadable", err.Error())
		return nil, "", false
	}

	comp := cat.findComponent(kind, name)
	if comp == nil {
		s.respondWireNotFound(w, namespace, name, "")
		return nil, "", false
	}
	// The requested namespace must equal the component's derived namespace.
	if deriveNamespace(comp.OwnerTeam) != namespace {
		s.respondWireNotFound(w, namespace, name, "")
		return nil, "", false
	}
	srcDir := filepath.Join(s.cfg.HubPath, filepath.FromSlash(comp.Path))
	if _, err := os.Stat(srcDir); err != nil {
		s.respondWireNotFound(w, namespace, name, "")
		return nil, "", false
	}
	return comp, srcDir, true
}

// --- helpers ---

// writeWireJSON emits a JSON body with ETag + Cache-Control and supports
// conditional revalidation via If-None-Match. immutable=true switches the
// cache directive to immutable (one year). immutable=false uses 60-second
// must-revalidate suitable for index and manifest.
func (s *Server) writeWireJSON(w http.ResponseWriter, r *http.Request, body []byte, immutable bool) {
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`

	cc := "public, max-age=60, must-revalidate"
	if immutable {
		cc = "public, max-age=31536000, immutable"
	}

	if r.Header.Get("If-None-Match") == etag {
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", cc)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", cc)
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *Server) respondWireNotFound(w http.ResponseWriter, namespace, name, version string) {
	body := map[string]string{
		"error":     "not_found",
		"namespace": namespace,
		"name":      name,
	}
	if version != "" {
		body["version"] = version
	}
	s.writeJSON(w, http.StatusNotFound, body)
}

func (s *Server) respondHubNotReady(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "5")
	s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"error":   "hub_not_ready",
		"message": "hub catalog not yet mounted at " + filepath.Join(s.cfg.HubPath, "hub", "registry.yaml"),
	})
}
