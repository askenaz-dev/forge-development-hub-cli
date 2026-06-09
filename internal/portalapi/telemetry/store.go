// Package telemetry is the portal's persistent usage-telemetry store
// (capability hub-usage-telemetry, Phase 2). It is the platform's FIRST
// database and holds ONLY telemetry/user-DATA — never catalog CONFIG. Per the
// "code = source of truth" invariant, nothing in this package reads or writes
// hub/registry.yaml, hub/harnesses.yaml, or any component directory.
//
// The store is STRICTLY OPTIONAL at boot (capability portal-runtime-resilience,
// design D1): an absent DSN or an unreachable Postgres MUST NOT crash the API
// or block anonymous catalog reads. Open never returns a fatal error for an
// unreachable store — it logs and returns a degraded noop whose Available()
// reports false. Callers branch on Available(): ingest best-effort-drops and
// admin reads surface a typed store_unavailable instead of a 500.
//
// PRIVACY (design D4): the Event carries a closed, minimal field set with NO
// PII — no username, email, hostname, IP, repo path, or file content. The only
// identifier is the pseudonymous, rotating, salted install_id, which is not
// reversible to an identity. The wire decoder (see decode.go) is strict and
// rejects unknown fields so a stray identity field can never be ingested.
package telemetry

import (
	"context"
	_ "embed"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaSQL string

// ErrStoreUnavailable is returned by read methods when the store is degraded
// (no DSN, or Postgres unreachable). Handlers translate it into a typed
// store_unavailable response with a Retry-After header rather than a 500.
var ErrStoreUnavailable = errors.New("telemetry store unavailable")

// Event is the closed, minimal, pseudonymous telemetry record. It is the
// single shape for every event type (install/download/resolve/activation/
// feedback). Fields that do not apply to a given event type are left zero.
//
// There is intentionally NO identity field on this struct. Adding one would
// violate design D4 and the no-PII guardrail test.
type Event struct {
	Event           string    // install|download|resolve|activation|feedback
	Kind            string    // skill|rule|agent|hook (component events)
	Namespace       string    // component namespace
	Name            string    // component name
	Version         string    // resolved/installed version
	ContentHash     string    // 64-hex content hash
	Scope           string    // user|project (install scope)
	Registry        string    // source registry
	OS              string    // coarse: darwin|linux|windows
	Locale          string    // es|en
	InstallID       string    // pseudonymous rotating salted hash (no identity)
	Step            string    // activation only: wizard step
	WizardSessionID string    // activation only: wizard session id
	Rating          *int      // feedback only: structured rating
	Category        string    // feedback only: structured category
	Text            string    // feedback only: free text
	Timestamp       time.Time // RFC3339 UTC client/server timestamp
}

// Store is the persistence boundary for telemetry. A live pgxStore talks to the
// shared Postgres; a degraded noopStore is returned when the DSN is empty or
// Postgres is unreachable so callers never crash.
//
// Aggregate read methods are intentionally minimal in Stage 1: ListActivation
// repoints the existing admin activation read. The Stage-2 admin analytics
// endpoints add their own aggregate readers; this interface grows additively.
type Store interface {
	// Insert persists one event. Returns an error on a degraded store or a
	// failed write; callers on the ingest path swallow it (best-effort drop).
	Insert(ctx context.Context, e Event) error

	// ListActivation returns the most recent activation events (newest first),
	// capped at limit. Returns ErrStoreUnavailable on a degraded store.
	ListActivation(ctx context.Context, limit int) ([]Event, error)

	// Aggregate recomputes the long-lived rollups and prunes raw events past
	// the retention window. Driven by the in-process loop on the elected
	// replica. A degraded store returns ErrStoreUnavailable (the loop pauses).
	Aggregate(ctx context.Context, retention time.Duration) error

	// Available reports whether the store is backed by a live database.
	Available() bool

	// Close releases the underlying pool.
	Close() error
}

// Open connects to Postgres at dsn, applies the embedded idempotent schema, and
// returns a live Store. It is NON-FATAL by contract: an empty dsn or an
// unreachable/failing Postgres yields a degraded noop Store (Available()==false)
// and a nil error, after logging. Boot never fails because of telemetry.
//
// The returned error is reserved for truly unexpected, non-connectivity faults;
// the server wiring treats even that as non-fatal (logs and proceeds with the
// noop). In practice Open returns (noop, nil) for every degraded path.
func Open(ctx context.Context, dsn string, logger *slog.Logger) (Store, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if dsn == "" {
		logger.Info("telemetry store disabled (no FDH_TELEMETRY_DSN); " +
			"ingest will best-effort drop and admin reads return store_unavailable")
		return newNoopStore(), nil
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		// A malformed DSN is an operator misconfiguration, but per D1 it MUST
		// NOT crash boot — degrade to noop and log loudly.
		logger.Warn("telemetry store DSN is malformed; running without a store",
			"err", err)
		return newNoopStore(), nil
	}

	// Bound the connect attempt so a hung/unreachable Postgres cannot stall
	// boot. The HTTP server and anonymous catalog come up regardless.
	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(connectCtx, cfg)
	if err != nil {
		logger.Warn("telemetry store connect failed; running without a store "+
			"(anonymous catalog unaffected)", "err", err)
		return newNoopStore(), nil
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		logger.Warn("telemetry store ping failed; running without a store "+
			"(anonymous catalog unaffected)", "err", err)
		return newNoopStore(), nil
	}

	if _, err := pool.Exec(connectCtx, schemaSQL); err != nil {
		pool.Close()
		logger.Warn("telemetry store schema apply failed; running without a "+
			"store (anonymous catalog unaffected)", "err", err)
		return newNoopStore(), nil
	}

	logger.Info("telemetry store ready")
	return &pgxStore{pool: pool, logger: logger}, nil
}
