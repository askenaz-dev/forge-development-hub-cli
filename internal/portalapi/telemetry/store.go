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

// EventCount pairs an event type with its total count, for the analytics
// summary's by_event breakdown. AGGREGATE only — never an identity row.
type EventCount struct {
	Event string
	Count int64
}

// ComponentCount is a per-component aggregate count for the top-installed /
// top-downloaded analytics views. AGGREGATE only — no install_id, no identity.
type ComponentCount struct {
	Kind      string
	Namespace string
	Name      string
	Count     int64
}

// TrendPoint is one (day, count) data point for the install-trends view.
type TrendPoint struct {
	Date  string // YYYY-MM-DD (UTC)
	Count int64
}

// FunnelStep is one onboarding-funnel step with its aggregate count.
type FunnelStep struct {
	Step  string
	Count int64
}

// ClaimedInstall is one install the signed-in user voluntarily claimed,
// surfaced in their profile activity feed (design D5). It is derived ONLY from
// the explicit install_claims link joined to that user's claimed install_ids —
// never by reversing a pseudonymous id.
type ClaimedInstall struct {
	Kind      string
	Name      string
	Version   string
	Timestamp time.Time
}

// Store is the persistence boundary for telemetry. A live pgxStore talks to the
// shared Postgres; a degraded noopStore is returned when the DSN is empty or
// Postgres is unreachable so callers never crash.
//
// Read methods that aggregate return ErrStoreUnavailable on a degraded store so
// handlers surface a typed store_unavailable (with Retry-After) instead of a
// 500. Every analytics read returns AGGREGATES ONLY — no method joins an
// install_id to an identity. The single, explicit identity↔telemetry link is
// the voluntary install claim (Claim / ActivityFor, design D5).
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

	// --- Stage-2 admin analytics reads (aggregates only) ---

	// SummaryByEvent returns the total event count and the per-event-type
	// breakdown for the whole retained window (or since the store's earliest
	// retained event). since reports the earliest event timestamp covered.
	SummaryByEvent(ctx context.Context) (total int64, byEvent []EventCount, since time.Time, err error)

	// TopComponents returns the most-counted components for a given metric
	// event ("install" or "download"), highest first, capped at limit.
	TopComponents(ctx context.Context, metric string, limit int) ([]ComponentCount, error)

	// Trends returns the per-day counts for the given event over the last
	// `days` days (UTC), oldest first.
	Trends(ctx context.Context, event string, days int) ([]TrendPoint, error)

	// Funnel returns the onboarding-funnel step counts (aggregated across all
	// days), highest first.
	Funnel(ctx context.Context) ([]FunnelStep, error)

	// EventCount returns the number of raw events currently retained (a store
	// health signal for the observability surface).
	EventCount(ctx context.Context) (int64, error)

	// --- Stage-2 feedback reads ---

	// ListFeedback returns persisted feedback events (newest first), paginated
	// by limit/offset, plus the total count.
	ListFeedback(ctx context.Context, limit, offset int) (items []Event, total int, err error)

	// --- Stage-2 profile activity (the ONLY identity↔telemetry link, D5) ---

	// Claim links a pseudonymous install_id to the signed-in user's email,
	// idempotently. This is the single, explicit, user-initiated mapping; it is
	// stored in the SEPARATE install_claims table, never joined into events.
	Claim(ctx context.Context, installID, userEmail string) error

	// ActivityFor returns the installs the given user voluntarily claimed,
	// newest first. It joins the user's claimed install_ids to the install
	// events for those ids — so install activity appears ONLY after a claim,
	// never by reversing an unclaimed id.
	ActivityFor(ctx context.Context, userEmail string, limit int) ([]ClaimedInstall, error)

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
