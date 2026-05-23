package portalapi

import (
	"context"
	_ "embed"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/falabella/fdh/pkg/bundle"
	"github.com/falabella/fdh/pkg/registry"
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

// handleGetSkill returns the full manifest for one skill.
func (s *Server) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("namespace")
	name := r.PathValue("name")

	man, err := s.registry.Manifest(r.Context(), ns, name)
	if err != nil {
		s.notFoundFor(w, "skill_not_found", ns, name, "")
		return
	}

	skillMDBase := strings.TrimRight(s.publicBaseURL(r), "/") + "/api/v1/skills/" + ns + "/" + name
	versions := make([]map[string]any, 0, len(man.Versions))
	for _, v := range man.Versions {
		versions = append(versions, versionToJSON(skillMDBase, v))
	}

	s.writeJSON(w, http.StatusOK, map[string]any{
		"namespace":   man.Namespace,
		"name":        man.Name,
		"description": man.Description,
		"owner_team":  man.OwnerTeam,
		"tags":        man.Tags,
		"latest":      man.Latest,
		"versions":    versions,
	})
}

// handleGetVersion returns one version's metadata.
func (s *Server) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	version := r.PathValue("version")

	man, err := s.registry.Manifest(r.Context(), ns, name)
	if err != nil {
		s.notFoundFor(w, "skill_not_found", ns, name, version)
		return
	}
	v := man.FindVersion(version)
	if v == nil {
		s.notFoundFor(w, "version_not_found", ns, name, version)
		return
	}
	skillMDBase := strings.TrimRight(s.publicBaseURL(r), "/") + "/api/v1/skills/" + ns + "/" + name
	s.writeJSON(w, http.StatusOK, versionToJSON(skillMDBase, *v))
}

// handleGetSkillMD serves the raw SKILL.md bytes from the bundle.
func (s *Server) handleGetSkillMD(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	version := r.PathValue("version")

	bp, err := s.registry.FetchBundle(r.Context(), ns, name, version)
	if err != nil {
		s.notFoundFor(w, "bundle_not_found", ns, name, version)
		return
	}
	defer bp.Cleanup()

	b, err := bundle.Load(bp.Path)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "bundle_load_failed", err.Error())
		return
	}

	// Read the raw SKILL.md from disk.
	skillPath := filepath.Join(b.Root, "SKILL.md")
	body, err := readFile(skillPath)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "skill_md_unreadable", err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
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
		"refreshed_at": snap.refreshedAt.Format(time.RFC3339),
		"skill_count":  len(snap.index.Skills),
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

func versionToJSON(skillMDBase string, v registry.Version) map[string]any {
	out := map[string]any{
		"version":      v.Version,
		"content_hash": v.ContentHash,
		"published_at": v.PublishedAt,
		"published_by": v.PublishedBy,
		"scan_status":  v.ScanStatus,
		"skill_md_url": skillMDBase + "/versions/" + v.Version + "/skill-md",
	}
	if v.ChangelogURL != "" {
		out["changelog_url"] = v.ChangelogURL
	}
	if v.Signature != "" {
		out["signature"] = v.Signature
	}
	return out
}

func containsString(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// userFromContext is kept for tests that pre-attach a UserIdentity directly
// to the context. Production code uses `userFromRequest` (auth_middleware.go)
// which understands the richer `auth.User` shape.
func (s *Server) userFromContext(ctx context.Context) UserIdentity {
	if u, ok := ctx.Value(userContextKey{}).(UserIdentity); ok {
		return u
	}
	return UserIdentity{Role: "anonymous"}
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
