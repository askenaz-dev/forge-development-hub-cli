package portalapi

import (
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/forge/fdh/pkg/registry"
)

// validKind reports whether kind is one of the four hub primitives.
func validKind(kind string) bool {
	switch kind {
	case "skill", "rule", "agent", "hook":
		return true
	}
	return false
}

// handleListComponents implements GET /api/v1/components — the kind-aware
// catalog across all four primitives. Filters: kind, q, namespace, tag,
// scan_status, limit, cursor. Omitting kind returns every kind. The skills
// endpoint (/api/v1/skills) is the kind=skill view of this catalog.
func (s *Server) handleListComponents(w http.ResponseWriter, r *http.Request) {
	snap := s.Snapshot()
	if snap == nil {
		s.writeError(w, http.StatusServiceUnavailable, "not_ready", "catalog not yet loaded")
		return
	}

	kind := r.URL.Query().Get("kind")
	if kind != "" && !validKind(kind) {
		s.writeError(w, http.StatusBadRequest, "invalid_kind", "kind must be one of: skill, rule, agent, hook")
		return
	}
	q := strings.ToLower(r.URL.Query().Get("q"))
	namespace := r.URL.Query().Get("namespace")
	tag := r.URL.Query().Get("tag")
	scanStatus := r.URL.Query().Get("scan_status")
	limit := parseLimit(r.URL.Query().Get("limit"))
	cursor := r.URL.Query().Get("cursor")

	items := make([]map[string]any, 0, len(snap.index.Components))
	for _, e := range snap.index.Components {
		if kind != "" && e.Kind != kind {
			continue
		}
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
				e.Kind, e.Namespace, e.Name, e.Description, strings.Join(e.Tags, " "),
			}, " "))
			if !strings.Contains(haystack, q) {
				continue
			}
		}
		items = append(items, componentEntryToSummary(e))
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i]["kind"].(string) != items[j]["kind"].(string) {
			return items[i]["kind"].(string) < items[j]["kind"].(string)
		}
		if items[i]["namespace"].(string) != items[j]["namespace"].(string) {
			return items[i]["namespace"].(string) < items[j]["namespace"].(string)
		}
		return items[i]["name"].(string) < items[j]["name"].(string)
	})

	// Tier 0: capture the demand gap (zero-result search) across all kinds.
	if q != "" && len(items) == 0 {
		s.emit(EventSearchZero, map[string]string{"query": q, "surface": "api", "kind": kind})
	}

	start := 0
	if cursor != "" {
		if n, err := parseOffsetCursor(cursor); err == nil && n >= 0 && n < len(items) {
			start = n
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

// handleGetComponent implements GET /api/v1/components/{kind}/{namespace}/{name}.
func (s *Server) handleGetComponent(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	if !validKind(kind) {
		s.componentNotFound(w, kind, ns, name, "")
		return
	}
	comp, vers, ok := s.hubComponentVersions(kind, name)
	if !ok || deriveNamespace(comp.OwnerTeam) != ns {
		s.componentNotFound(w, kind, ns, name, "")
		return
	}
	base := strings.TrimRight(s.publicBaseURL(r), "/") + "/api/v1/components/" + kind + "/" + ns + "/" + name
	versions := make([]map[string]any, 0, len(vers))
	for _, v := range vers {
		versions = append(versions, componentVersionToJSON(base, v))
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"kind":        kind,
		"namespace":   ns,
		"name":        comp.Name,
		"description": comp.Description,
		"owner_team":  comp.OwnerTeam,
		"tags":        comp.Tags,
		"latest":      vers[0].Version,
		"versions":    versions,
	})
}

// handleGetComponentVersion implements
// GET /api/v1/components/{kind}/{namespace}/{name}/versions/{version}.
func (s *Server) handleGetComponentVersion(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	version := r.PathValue("version")
	if !validKind(kind) {
		s.componentNotFound(w, kind, ns, name, version)
		return
	}
	comp, vers, ok := s.hubComponentVersions(kind, name)
	if !ok || deriveNamespace(comp.OwnerTeam) != ns {
		s.componentNotFound(w, kind, ns, name, version)
		return
	}
	base := strings.TrimRight(s.publicBaseURL(r), "/") + "/api/v1/components/" + kind + "/" + ns + "/" + name
	for _, v := range vers {
		if v.Version == version {
			s.writeJSON(w, http.StatusOK, componentVersionToJSON(base, v))
			return
		}
	}
	s.componentNotFound(w, kind, ns, name, version)
}

// handleGetComponentDocument implements
// GET /api/v1/components/{kind}/{namespace}/{name}/versions/{version}/document,
// serving the kind's entrypoint markdown (SKILL.md/RULE.md/AGENT.md/HOOK.md).
func (s *Server) handleGetComponentDocument(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	ns := r.PathValue("namespace")
	name := r.PathValue("name")
	version := r.PathValue("version")
	if !validKind(kind) {
		s.componentNotFound(w, kind, ns, name, version)
		return
	}
	cat, err := loadHubCatalog(s.cfg.HubPath)
	if err != nil {
		s.componentNotFound(w, kind, ns, name, version)
		return
	}
	comp := cat.findComponent(kind, name)
	if comp == nil || deriveNamespace(comp.OwnerTeam) != ns {
		s.componentNotFound(w, kind, ns, name, version)
		return
	}
	srcDir := filepath.Join(s.cfg.HubPath, filepath.FromSlash(comp.Path))
	dir, cleanup, _, found, err := resolveVersionSource(s.cfg.HubPath, srcDir, comp.Path, comp.Kind, comp.Name, version)
	if err != nil || !found {
		s.componentNotFound(w, kind, ns, name, version)
		return
	}
	defer cleanup()

	body, err := readFile(filepath.Join(dir, entrypointFile(kind)))
	if err != nil {
		// Bundles may name the canonical document generically as SKILL.md.
		if alt, altErr := readFile(filepath.Join(dir, "SKILL.md")); altErr == nil {
			body = alt
		} else {
			s.writeError(w, http.StatusInternalServerError, "document_unreadable", err.Error())
			return
		}
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// --- helpers ---

func componentEntryToSummary(e registry.IndexEntry) map[string]any {
	return map[string]any{
		"kind":           e.Kind,
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

func componentVersionToJSON(base string, v resolvedVersion) map[string]any {
	out := map[string]any{
		"version":      v.Version,
		"content_hash": v.ContentHash,
		"published_at": v.PublishedAt.UTC().Format(time.RFC3339),
		"published_by": wirePublishedBy,
		"scan_status":  wireScanStatus,
		"document_url": base + "/versions/" + v.Version + "/document",
	}
	if v.Signature != "" {
		out["signature"] = v.Signature
	}
	return out
}

func (s *Server) componentNotFound(w http.ResponseWriter, kind, ns, name, version string) {
	body := map[string]string{
		"error":     "component_not_found",
		"kind":      kind,
		"namespace": ns,
		"name":      name,
	}
	if version != "" {
		body["version"] = version
	}
	s.emit(EventComponentMissing, map[string]string{
		"kind": kind, "namespace": ns, "name": name, "version": version,
	})
	s.writeJSON(w, http.StatusNotFound, body)
}
