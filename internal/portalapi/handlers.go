package portalapi

import (
	_ "embed"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/forge/fdh/pkg/registry"
)

//go:embed openapi.yaml
var openAPISpec []byte

// handleHealthz is always 200 if the process is running. Liveness probe.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz returns 200 only after the first successful registry read.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if !s.ready.Load() {
		s.writeError(w, http.StatusServiceUnavailable, "not_ready", "registry not yet loaded")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// handleListSkills implements GET /api/v1/skills with filter + pagination.
func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	snap := s.Snapshot()
	if snap == nil {
		s.writeError(w, http.StatusServiceUnavailable, "not_ready", "registry not yet loaded")
		return
	}

	q := strings.ToLower(r.URL.Query().Get("q"))
	namespace := r.URL.Query().Get("namespace")
	tag := r.URL.Query().Get("tag")
	scanStatus := r.URL.Query().Get("scan_status")
	limit := parseLimit(r.URL.Query().Get("limit"))
	cursor := r.URL.Query().Get("cursor")

	items := make([]map[string]any, 0, len(snap.index.Skills))
	for _, e := range snap.index.Skills {
		if namespace != "" && e.Namespace != namespace {
			continue
		}
		if scanStatus != "" && e.ScanStatus != scanStatus {
			continue
		}
		if tag != "" && !containsString(e.Tags, tag) {
			continue
		}
		if q != "" {
			haystack := strings.ToLower(strings.Join([]string{
				e.Namespace, e.Name, e.Description, strings.Join(e.Tags, " "),
			}, " "))
			if !strings.Contains(haystack, q) {
				continue
			}
		}
		items = append(items, indexEntryToSummary(e))
	}

	// Stable sort: namespace, then name.
	sort.SliceStable(items, func(i, j int) bool {
		ai, aj := items[i], items[j]
		if ai["namespace"].(string) != aj["namespace"].(string) {
			return ai["namespace"].(string) < aj["namespace"].(string)
		}
		return ai["name"].(string) < aj["name"].(string)
	})

	// Cursor pagination — opaque offset for the MVP.
	start := 0
	if cursor != "" {
		// Cursor is the index of the first item on the next page.
		if n, err := parseOffsetCursor(cursor); err == nil {
			if n >= 0 && n < len(items) {
				start = n
			}
		}
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	page := items[start:end]
	var nextCursor any
	if end < len(items) {
		nextCursor = encodeOffsetCursor(end)
	}

	s.writeJSON(w, http.StatusOK, map[string]any{
		"items":       page,
		"next_cursor": nextCursor,
	})
}

// handleGetSkill returns the full manifest for one skill, assembled from the
// real hub catalog. Skills are served under the "forge" namespace sentinel.
func (s *Server) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	comp, vers, ok := s.hubComponentVersions("skill", name)
	if !ok || deriveNamespace(comp.OwnerTeam) != ns {
		s.notFoundFor(w, "skill_not_found", ns, name, "")
		return
	}
	base := strings.TrimRight(s.publicBaseURL(r), "/") + "/api/v1/skills/" + ns + "/" + name
	versions := make([]map[string]any, 0, len(vers))
	for _, v := range vers {
		versions = append(versions, hubVersionToJSON(base, v))
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"namespace":   ns,
		"name":        comp.Name,
		"description": comp.Description,
		"owner_team":  comp.OwnerTeam,
		"tags":        comp.Tags,
		"latest":      vers[0].Version,
		"versions":    versions,
	})
}

// handleGetVersion returns one version's metadata.
func (s *Server) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	version := r.PathValue("version")
	comp, vers, ok := s.hubComponentVersions("skill", name)
	if !ok || deriveNamespace(comp.OwnerTeam) != ns {
		s.notFoundFor(w, "skill_not_found", ns, name, version)
		return
	}
	base := strings.TrimRight(s.publicBaseURL(r), "/") + "/api/v1/skills/" + ns + "/" + name
	for _, v := range vers {
		if v.Version == version {
			s.writeJSON(w, http.StatusOK, hubVersionToJSON(base, v))
			return
		}
	}
	s.notFoundFor(w, "version_not_found", ns, name, version)
}

// handleGetSkillMD serves the raw SKILL.md bytes for a version from the hub
// working tree (or the historical tree for an older tagged version).
func (s *Server) handleGetSkillMD(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	version := r.PathValue("version")
	cat, err := loadHubCatalog(s.cfg.HubPath)
	if err != nil {
		s.notFoundFor(w, "bundle_not_found", ns, name, version)
		return
	}
	comp := cat.findComponent("skill", name)
	if comp == nil || deriveNamespace(comp.OwnerTeam) != ns {
		s.notFoundFor(w, "bundle_not_found", ns, name, version)
		return
	}
	srcDir := filepath.Join(s.cfg.HubPath, filepath.FromSlash(comp.Path))
	dir, cleanup, _, found, err := resolveVersionSource(s.cfg.HubPath, srcDir, comp.Path, comp.Kind, comp.Name, version)
	if err != nil || !found {
		s.notFoundFor(w, "version_not_found", ns, name, version)
		return
	}
	defer cleanup()

	body, err := readFile(filepath.Join(dir, entrypointFile("skill")))
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "skill_md_unreadable", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// hubComponentVersions resolves a component of the given kind from the real
// hub catalog plus its published versions (newest first). ok is false when
// the component or its versions cannot be resolved.
func (s *Server) hubComponentVersions(kind, name string) (*hubComponent, []resolvedVersion, bool) {
	cat, err := loadHubCatalog(s.cfg.HubPath)
	if err != nil {
		return nil, nil, false
	}
	comp := cat.findComponent(kind, name)
	if comp == nil {
		return nil, nil, false
	}
	srcDir := filepath.Join(s.cfg.HubPath, filepath.FromSlash(comp.Path))
	vers, err := componentVersions(s.cfg.HubPath, srcDir, comp.Path, comp.Kind, comp.Name)
	if err != nil || len(vers) == 0 {
		return nil, nil, false
	}
	return comp, vers, true
}

// hubVersionToJSON renders a resolvedVersion as the /api/v1 version JSON shape.
func hubVersionToJSON(base string, v resolvedVersion) map[string]any {
	out := map[string]any{
		"version":      v.Version,
		"content_hash": v.ContentHash,
		"published_at": v.PublishedAt.UTC().Format(time.RFC3339),
		"published_by": wirePublishedBy,
		"scan_status":  wireScanStatus,
		"skill_md_url": base + "/versions/" + v.Version + "/skill-md",
	}
	if v.Signature != "" {
		out["signature"] = v.Signature
	}
	return out
}

// handleAuthMe is the identity endpoint. Without OIDC configured, every
// caller is anonymous; the middleware populates the user object on the
// request context, which we serialize here.
func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	u := userFromRequest(r)
	s.writeJSON(w, http.StatusOK, UserIdentity{
		Role:   u.Role,
		Sub:    u.Sub,
		Name:   u.Name,
		Email:  u.Email,
		Claims: u.Claims,
	})
}

// handleRefresh forces an immediate registry refresh. Requires role
// publisher or above when auth is enabled; without auth, any caller can
// trigger it (development convenience).
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AuthEnabled() {
		u := userFromRequest(r)
		if !hasMinRole(u.Role, "publisher") {
			s.writeError(w, http.StatusForbidden, "forbidden", "role 'publisher' or above required")
			return
		}
	}
	if err := s.Refresh(r.Context()); err != nil {
		s.writeError(w, http.StatusInternalServerError, "refresh_failed", err.Error())
		return
	}
	snap := s.Snapshot()
	s.writeJSON(w, http.StatusOK, map[string]any{
		"refreshed_at":    snap.refreshedAt.Format(time.RFC3339),
		"component_count": len(snap.index.Components),
		"skill_count":     len(snap.index.Skills),
	})
}

// handleOpenAPI serves the embedded OpenAPI spec for tools to consume.
func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(openAPISpec)
}

// --- helpers ---

func (s *Server) notFoundFor(w http.ResponseWriter, code, ns, name, version string) {
	body := map[string]string{
		"error":     code,
		"namespace": ns,
		"name":      name,
	}
	if version != "" {
		body["version"] = version
	}
	s.writeJSON(w, http.StatusNotFound, body)
}

func (s *Server) publicBaseURL(r *http.Request) string {
	// Honor X-Forwarded-Host / Proto if the proxy set them; else r.Host.
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		proto = "http"
		if r.TLS != nil {
			proto = "https"
		}
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return proto + "://" + host
}

func indexEntryToSummary(e registry.IndexEntry) map[string]any {
	return map[string]any{
		"namespace":      e.Namespace,
		"name":           e.Name,
		"description":    e.Description,
		"owner_team":     e.OwnerTeam,
		"tags":           e.Tags,
		"latest_version": e.LatestVersion,
		"latest_hash":    e.LatestHash,
		"scan_status":    e.ScanStatus,
	}
}

func containsString(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// hasMinRole reports whether actual satisfies the minimum role required.
func hasMinRole(actual, required string) bool {
	return roleRank(actual) >= roleRank(required)
}

func roleRank(role string) int {
	switch role {
	case "admin":
		return 5
	case "publisher":
		return 4
	case "reviewer":
		return 3
	case "author":
		return 2
	case "consumer":
		return 1
	case "anonymous":
		return 0
	}
	return 0
}

type userContextKey struct{}

// UserIdentity is the wire shape of /api/v1/auth/me.
type UserIdentity struct {
	Role   string   `json:"role"`
	Sub    string   `json:"sub,omitempty"`
	Name   string   `json:"name,omitempty"`
	Email  string   `json:"email,omitempty"`
	Claims []string `json:"claims,omitempty"`
}
