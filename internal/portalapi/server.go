package portalapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/forge/fdh/internal/portalapi/auth"
	"github.com/forge/fdh/pkg/registry"
	"github.com/forge/fdh/pkg/scan"
)

// Server is the long-lived API server backed by a `pkg/registry.Registry`.
// All read endpoints serve from an in-memory snapshot maintained by a
// background refresh loop.
type Server struct {
	cfg   Config
	build BuildInfo

	snapshot atomic.Pointer[snapshot]
	mu       sync.Mutex
	ready    atomic.Bool

	logger     *slog.Logger
	metrics    *metricsRegistry
	validator  tokenValidator // nil when auth is disabled
	activation *activationRing

	// scanCache memoizes the fdh-scan verdict per component bundle, keyed by
	// canonical content hash so an unchanged bundle is scanned once across
	// refreshes and requests (capability portal-scan-status, decision D1).
	scanMu    sync.RWMutex
	scanCache map[string]string
}

type snapshot struct {
	index       registry.Index
	indexByKey  map[string]registry.IndexEntry // "kind/ns/name" → entry
	refreshedAt time.Time
}

// tokenValidator abstracts bearer-token validation so the server can hold
// either an eager *auth.Validator or an *auth.LazyValidator that tolerates an
// IdP which is unavailable at startup. Production uses the lazy variant so a
// missing/unhealthy realm cannot crash the boot path.
type tokenValidator interface {
	Validate(ctx context.Context, rawToken string) (auth.User, error)
}

// New constructs the server. It does NOT perform the first registry read;
// that happens asynchronously in RunRefreshLoop so the HTTP server can
// start serving /healthz immediately.
func New(cfg Config, build BuildInfo) (*Server, error) {
	var validator tokenValidator
	if cfg.AuthEnabled() {
		rm, err := auth.LoadRoleMap(cfg.OIDCRoleMapPath)
		if err != nil {
			return nil, fmt.Errorf("load role map: %w", err)
		}
		// Build the OIDC validator LAZILY. The portal API is a read-only,
		// anonymous-by-default catalog; its availability must NOT be coupled
		// to the IdP being healthy at boot. Constructing the validator
		// eagerly here and failing startup on error meant a transient IdP
		// outage — or a Keycloak realm that vanished on restart — crash-looped
		// the API and 503'd the entire catalog on every restart. The lazy
		// validator initializes on first token use and self-heals once the
		// IdP recovers, with no process restart. We attempt one best-effort
		// warm-up so a healthy IdP is ready immediately, but never fail boot.
		lv := auth.NewLazy(cfg.OIDCDiscoveryURL, cfg.OIDCClientID, rm)
		if err := lv.Warm(context.Background()); err != nil {
			slog.Default().Warn("OIDC discovery unreachable at startup; "+
				"anonymous catalog stays available and token auth will "+
				"initialize automatically once the IdP is reachable",
				"discovery_url", cfg.OIDCDiscoveryURL, "err", err)
		}
		validator = lv
	}

	return &Server{
		cfg:        cfg,
		build:      build,
		logger:     slog.Default(),
		metrics:    newMetrics(),
		validator:  validator,
		activation: newActivationRing(512),
		scanCache:  make(map[string]string),
	}, nil
}

// buildHubIndex builds the in-memory catalog snapshot from the real hub
// content at hubPath (hub/registry.yaml + the four primitive directories),
// reusing the same producer machinery (loadHubCatalog + componentVersions)
// as the wire endpoints. Components of every kind are included; Skills is the
// kind=="skill" projection so the /api/v1/skills endpoint stays skill-scoped.
// namespace is derived from each component's owner_team per the
// hub-http-registry canonical rule (deriveNamespace). Each entry's scan_status
// is the real fdh-scan verdict over the component's tip bundle (cached by
// content hash), so the served catalog reflects security posture, not a
// sentinel (capability portal-scan-status).
func (s *Server) buildHubIndex(hubPath string) (registry.Index, error) {
	cat, err := loadHubCatalog(hubPath)
	if err != nil {
		return registry.Index{}, err
	}
	idx := registry.Index{SchemaVersion: 2, Registry: "forge-development-hub"}
	for _, comp := range cat.Components {
		switch comp.Kind {
		case "skill", "rule", "agent", "hook":
		default:
			continue
		}
		srcDir := filepath.Join(hubPath, filepath.FromSlash(comp.Path))
		vers, err := componentVersions(hubPath, srcDir, comp.Path, comp.Kind, comp.Name)
		if err != nil || len(vers) == 0 {
			continue
		}
		latest := vers[0]
		idx.Components = append(idx.Components, registry.IndexEntry{
			Kind:          comp.Kind,
			Namespace:     deriveNamespace(comp.OwnerTeam),
			Name:          comp.Name,
			Description:   comp.Description,
			OwnerTeam:     comp.OwnerTeam,
			Tags:          comp.Tags,
			LatestVersion: latest.Version,
			LatestHash:    latest.ContentHash,
			ScanStatus:    s.scanStatusFor(latest.ContentHash, srcDir),
		})
	}
	for _, e := range idx.Components {
		if e.Kind == "skill" {
			idx.Skills = append(idx.Skills, e)
		}
	}
	return idx, nil
}

// Handler returns the configured http.Handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)

	// Wire-protocol endpoints (consumed by pkg/registry.HTTPRegistry).
	mux.HandleFunc("GET /v1/index.json", s.handleWireIndex)
	mux.HandleFunc("GET /v1/{kindPlural}/{namespace}/{name}/manifest.json", s.handleWireManifest)
	mux.HandleFunc("GET /v1/{kindPlural}/{namespace}/{name}/versions/{version}/bundle.tar.gz", s.handleWireBundleTarball)
	mux.HandleFunc("GET /v1/{kindPlural}/{namespace}/{name}/versions/{version}/bundle.sha256", s.handleWireBundleSidecar)

	mux.HandleFunc("GET /api/v1/skills", s.handleListSkills)
	mux.HandleFunc("GET /api/v1/skills/{namespace}/{name}", s.handleGetSkill)
	mux.HandleFunc("GET /api/v1/skills/{namespace}/{name}/versions/{version}", s.handleGetVersion)
	mux.HandleFunc("GET /api/v1/skills/{namespace}/{name}/versions/{version}/skill-md", s.handleGetSkillMD)

	// Kind-aware component catalog (skills are the kind=skill view above).
	mux.HandleFunc("GET /api/v1/components", s.handleListComponents)
	mux.HandleFunc("GET /api/v1/components/{kind}/{namespace}/{name}", s.handleGetComponent)
	mux.HandleFunc("GET /api/v1/components/{kind}/{namespace}/{name}/versions/{version}", s.handleGetComponentVersion)
	mux.HandleFunc("GET /api/v1/components/{kind}/{namespace}/{name}/versions/{version}/document", s.handleGetComponentDocument)
	mux.HandleFunc("GET /api/v1/auth/me", s.handleAuthMe)
	mux.HandleFunc("POST /api/v1/refresh", s.handleRefresh)
	mux.HandleFunc("POST /api/v1/activation", s.handlePostActivation)
	mux.HandleFunc("GET /api/v1/admin/activation", s.handleGetActivation)

	mux.HandleFunc("GET /openapi.yaml", s.handleOpenAPI)
	// API documentation UIs.
	mux.HandleFunc("GET /docs", s.handleDocsIndex)
	mux.HandleFunc("GET /docs/", s.handleDocsIndex)
	mux.HandleFunc("GET /docs/swagger", s.handleDocsIndex)
	mux.HandleFunc("GET /docs/redoc", s.handleDocsIndex)
	mux.HandleFunc("GET /redoc", s.handleRedoc)
	mux.Handle("GET /metrics", s.metrics.handler())

	// Order: auth attaches user to context first; logging captures the
	// user_id; metrics observes the wrapped handler.
	return s.withRequestLogging(s.withAuth(s.withMetrics(mux)))
}

// RunRefreshLoop performs the initial registry read and then refreshes on
// the configured interval until ctx is canceled. Failures are logged but
// non-fatal — the previous snapshot continues to serve.
func (s *Server) RunRefreshLoop(ctx context.Context) {
	if err := s.Refresh(ctx); err != nil {
		s.logger.Error("initial refresh failed; will retry", "err", err)
	}
	ticker := time.NewTicker(s.cfg.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Refresh(ctx); err != nil {
				s.logger.Warn("scheduled refresh failed", "err", err)
			}
		}
	}
}

// Refresh re-reads the registry into a fresh snapshot. Safe to call
// concurrently — the mutex serializes refreshes; reads remain lock-free
// via the atomic pointer.
func (s *Server) Refresh(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	start := time.Now()
	// Ensure the hub content is present at HubPath. This deployment shares a
	// single checkout dir (no git-sync sidecar): clone/refresh the content
	// repo into HubPath, then build the catalog from its working tree.
	if s.cfg.RegistryURL != "" {
		syncReg := &registry.GitRegistry{
			LocalPath: s.cfg.HubPath,
			RemoteURL: s.cfg.RegistryURL,
			Branch:    s.cfg.RegistryBranch,
			Logger:    func(line string) { s.logger.Info("hub sync", "msg", line) },
		}
		if err := syncReg.Sync(ctx); err != nil {
			s.logger.Warn("hub content sync failed; building from on-disk content", "err", err)
		}
	}
	idx, err := s.buildHubIndex(s.cfg.HubPath)
	if err != nil {
		if s.metrics != nil {
			s.metrics.refreshTotal.WithLabelValues("error").Inc()
		}
		return fmt.Errorf("build hub index: %w", err)
	}
	snap := &snapshot{
		index:       idx,
		indexByKey:  make(map[string]registry.IndexEntry, len(idx.Components)),
		refreshedAt: time.Now().UTC(),
	}
	for _, e := range idx.Components {
		key := e.Kind + "/" + e.Namespace + "/" + e.Name
		snap.indexByKey[key] = e
	}
	s.snapshot.Store(snap)
	s.ready.Store(true)
	if s.metrics != nil {
		s.metrics.refreshTotal.WithLabelValues("ok").Inc()
		s.metrics.refreshDuration.Observe(time.Since(start).Seconds())
		s.metrics.registryCacheSize.Set(float64(len(idx.Components)))
	}
	s.logger.Info("catalog refreshed",
		"component_count", len(idx.Components),
		"skill_count", len(idx.Skills),
		"refreshed_at", snap.refreshedAt.Format(time.RFC3339))
	return nil
}

// Snapshot returns the current immutable view of the registry. nil means
// no read has succeeded yet.
func (s *Server) Snapshot() *snapshot {
	return s.snapshot.Load()
}

// scanStatusFor returns the fdh-scan verdict (pass|warn|fail) for a component
// bundle at srcDir, memoized by its canonical content hash so an unchanged
// bundle is scanned at most once across refreshes and requests. A scan that
// cannot run yields "none" and is NOT cached, so it is retried next time
// (capability portal-scan-status, decisions D1/D2; scan errors never abort).
func (s *Server) scanStatusFor(contentHash, srcDir string) string {
	if contentHash != "" {
		s.scanMu.RLock()
		v, ok := s.scanCache[contentHash]
		s.scanMu.RUnlock()
		if ok {
			return v
		}
	}
	st, err := scan.DirStatus(srcDir)
	if err != nil {
		s.logger.Warn("scan failed; recording none", "dir", srcDir, "err", err)
		return scan.StatusNone
	}
	if contentHash != "" {
		s.scanMu.Lock()
		s.scanCache[contentHash] = st
		s.scanMu.Unlock()
	}
	return st
}

// componentScanStatus is the verdict for a component's latest (tip) version,
// scanned from its working-tree source. Older tagged versions are not
// re-scanned here (assume-latest, per the change's open questions); callers
// map them to "none".
func (s *Server) componentScanStatus(comp *hubComponent, vers []resolvedVersion) string {
	if comp == nil || len(vers) == 0 {
		return scan.StatusNone
	}
	srcDir := filepath.Join(s.cfg.HubPath, filepath.FromSlash(comp.Path))
	return s.scanStatusFor(vers[0].ContentHash, srcDir)
}

// versionScanStatus maps a per-version scan_status: the tip version carries the
// component verdict; older versions are "none" (not re-scanned).
func versionScanStatus(v resolvedVersion, latest, compStatus string) string {
	if v.Version == latest {
		return compStatus
	}
	return scan.StatusNone
}

// --- helpers ---

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Warn("write JSON failed", "err", err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, status int, code, message string) {
	s.writeJSON(w, status, map[string]string{
		"error":   code,
		"message": message,
	})
}

// parseLimit clamps the limit query parameter to [1, 200] with default 50.
func parseLimit(q string) int {
	if q == "" {
		return 50
	}
	n, err := strconv.Atoi(q)
	if err != nil || n < 1 {
		return 50
	}
	if n > 200 {
		return 200
	}
	return n
}
