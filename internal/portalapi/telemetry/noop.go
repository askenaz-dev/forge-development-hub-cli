package telemetry

import (
	"context"
	"time"
)

// noopStore is the degraded store returned when no DSN is configured or
// Postgres is unreachable (design D1, portal-runtime-resilience). It never
// errors on Insert (the ingest path treats writes as best-effort and drops),
// and read/aggregate paths return ErrStoreUnavailable so handlers can emit a
// typed store_unavailable response instead of a 500. Available() is false.
type noopStore struct{}

func newNoopStore() *noopStore { return &noopStore{} }

// Insert silently drops the event. Best-effort by contract: ingest must never
// fail a client because the store is down.
func (n *noopStore) Insert(ctx context.Context, e Event) error { return nil }

// ListActivation signals unavailability so the read path degrades gracefully.
func (n *noopStore) ListActivation(ctx context.Context, limit int) ([]Event, error) {
	return nil, ErrStoreUnavailable
}

// Aggregate is a no-op on a degraded store; the loop pauses.
func (n *noopStore) Aggregate(ctx context.Context, retention time.Duration) error {
	return ErrStoreUnavailable
}

// All Stage-2 analytics/feedback/activity reads signal unavailability so the
// admin handlers degrade to a typed store_unavailable (with Retry-After) rather
// than a 500 (portal-runtime-resilience).

func (n *noopStore) SummaryByEvent(ctx context.Context) (int64, []EventCount, time.Time, error) {
	return 0, nil, time.Time{}, ErrStoreUnavailable
}

func (n *noopStore) TopComponents(ctx context.Context, metric string, limit int) ([]ComponentCount, error) {
	return nil, ErrStoreUnavailable
}

func (n *noopStore) Trends(ctx context.Context, event string, days int) ([]TrendPoint, error) {
	return nil, ErrStoreUnavailable
}

func (n *noopStore) Funnel(ctx context.Context) ([]FunnelStep, error) {
	return nil, ErrStoreUnavailable
}

func (n *noopStore) EventCount(ctx context.Context) (int64, error) {
	return 0, ErrStoreUnavailable
}

func (n *noopStore) ListFeedback(ctx context.Context, limit, offset int) ([]Event, int, error) {
	return nil, 0, ErrStoreUnavailable
}

// Claim cannot persist on a degraded store. Unlike Insert (best-effort drop on
// the anonymous ingest path), a claim is an explicit user action whose success
// the caller reports, so it surfaces the unavailability.
func (n *noopStore) Claim(ctx context.Context, installID, userEmail string) error {
	return ErrStoreUnavailable
}

func (n *noopStore) ActivityFor(ctx context.Context, userEmail string, limit int) ([]ClaimedInstall, error) {
	return nil, ErrStoreUnavailable
}

func (n *noopStore) Available() bool { return false }

func (n *noopStore) Close() error { return nil }
