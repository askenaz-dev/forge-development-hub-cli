package portalapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/falabella/fdh/internal/portalapi/auth"
	"github.com/falabella/fdh/pkg/registry"
)

// Server is the long-lived API server backed by a `pkg/registry.Registry`.
// All read endpoints serve from an in-memory snapshot maintained by a
// background refresh loop.
type Server struct {
	cfg       Config
	build     BuildInfo
	registry  registry.Registry

	snapshot atomic.Pointer[snapshot]
	mu       sync.Mutex
	ready    atomic.Bool

	logger     *slog.Logger
	metrics    *metricsRegistry
	validator  *auth.Validator // nil when auth is disabled
	activation *activationRing
}

type snapshot struct {
	index       registry.Index
	indexByKey  map[string]registry.IndexEntry // "ns/name" → entry
	refreshedAt time.Time
}

// New constructs the server. It does NOT perform the first registry read;
// that happens asynchronously in RunRefreshLoop so the HTTP server can
// start serving /healthz immediately.
func New(cfg Config, build BuildInfo) (*Server, error) {
	reg := buildRegistry(cfg)

	var validator *auth.Validator
	if cfg.AuthEnabled() {
		rm, err := auth.LoadRoleMap(cfg.OIDCRoleMapPath)
		if err != nil {
			return nil, fmt.Errorf("load role map: %w", err)
		}
		v, err := auth.New(context.Background(), cfg.OIDCDiscoveryURL, cfg.OIDCClientID, rm)
		if err != nil {
			return nil, fmt.Errorf("oidc validator: %w", err)
		}
		validator = v
	}

	return &Server{
		cfg:        cfg,
		build:      build,
		registry:   reg,
		logger:     slog.Default(),
		metrics:    newMetrics(),
		validator:  validator,
		activation: newActivationRing(512),
	}, nil
}

func buildRegistry(cfg Config) registry.Registry {
	localPath := cfg.RegistryLocalPath
	if localPath == "" {
		// Derive a clone path from the URL for cache locality.
		localPath = filepath.Join(os.TempDir(), "fdh-portal-registry-cache")
	}
	return &registry.GitRegistry{
		LocalPath: localPath,
		RemoteURL: cfg.RegistryURL,
		Branch:    cfg.RegistryBranch,
		Logger: func(line string) {
			slog.Default().Info("registry", "msg", line)
		},
	}
}

// Handler returns the configured http.Handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)

	mux.HandleFunc("GET /api/v1/skills", s.handleListSkills)
	mux.HandleFunc("GET /api/v1/skills/{namespace}/{name}", s.handleGetSkill)
	mux.HandleFunc("GET /api/v1/skills/{namespace}/{name}/versions/{version}", s.handleGetVersion)
	mux.HandleFunc("GET /api/v1/skills/{namespace}/{name}/versions/{version}/skill-md", s.handleGetSkillMD)
	mux.HandleFunc("GET /api/v1/auth/me", s.handleAuthMe)
	mux.HandleFunc("POST /api/v1/refresh", s.handleRefresh)
	mux.HandleFunc("POST /api/v1/activation", s.handlePostActivation)
	mux.HandleFunc("GET /api/v1/admin/activation", s.handleGetActivation)

	mux.HandleFunc("GET /openapi.yaml", s.handleOpenAPI)
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
	idx, err := s.registry.Index(ctx)
	if err != nil {
		if s.metrics != nil {
			s.metrics.refreshTotal.WithLabelValues("error").Inc()
		}
		return fmt.Errorf("registry index: %w", err)
	}
	snap := &snapshot{
		index:       idx,
		indexByKey:  make(map[string]registry.IndexEntry, len(idx.Skills)),
		refreshedAt: time.Now().UTC(),
	}
	for _, e := range idx.Skills {
		key := e.Namespace + "/" + e.Name
		snap.indexByKey[key] = e
	}
	s.snapshot.Store(snap)
	s.ready.Store(true)
	if s.metrics != nil {
		s.metrics.refreshTotal.WithLabelValues("ok").Inc()
		s.metrics.refreshDuration.Observe(time.Since(start).Seconds())
		s.metrics.registryCacheSize.Set(float64(len(idx.Skills)))
	}
	s.logger.Info("registry refreshed",
		"skill_count", len(idx.Skills),
		"refreshed_at", snap.refreshedAt.Format(time.RFC3339))
	return nil
}

// Snapshot returns the current immutable view of the registry. nil means
// no read has succeeded yet.
func (s *Server) Snapshot() *snapshot {
	return s.snapshot.Load()
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
