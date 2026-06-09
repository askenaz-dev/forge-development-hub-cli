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

func (n *noopStore) Available() bool { return false }

func (n *noopStore) Close() error { return nil }
