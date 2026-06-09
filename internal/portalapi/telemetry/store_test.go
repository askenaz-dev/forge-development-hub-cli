package telemetry

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// TestOpen_NoDSN_IsDegradedNoop proves the non-fatal contract: an empty DSN
// yields a usable degraded store, never an error. (§11.3 store-outage / D1)
func TestOpen_NoDSN_IsDegradedNoop(t *testing.T) {
	st, err := Open(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("Open with empty DSN must not error, got %v", err)
	}
	if st == nil {
		t.Fatal("Open must return a non-nil store even with no DSN")
	}
	if st.Available() {
		t.Fatal("a store with no DSN must report Available()==false")
	}
}

// TestOpen_UnreachableDSN_IsDegradedNoop proves an unreachable Postgres degrades
// to noop without crashing boot. The DSN points at a closed port; the bounded
// connect attempt fails and Open returns (noop, nil).
func TestOpen_UnreachableDSN_IsDegradedNoop(t *testing.T) {
	// 127.0.0.1:1 is reserved/closed; connect fails fast within the timeout.
	st, err := Open(context.Background(),
		"postgres://fdh:fdh@127.0.0.1:1/fdh?sslmode=disable", nil)
	if err != nil {
		t.Fatalf("Open with unreachable DSN must not error, got %v", err)
	}
	if st.Available() {
		t.Fatal("unreachable store must report Available()==false")
	}
}

// TestNoopStore_Behavior pins the degraded-store contract: Insert silently
// succeeds (best-effort drop), reads/aggregate return ErrStoreUnavailable.
func TestNoopStore_Behavior(t *testing.T) {
	st := newNoopStore()
	if err := st.Insert(context.Background(), Event{Event: "install"}); err != nil {
		t.Fatalf("noop Insert must not error (best-effort drop), got %v", err)
	}
	if _, err := st.ListActivation(context.Background(), 10); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("noop ListActivation must return ErrStoreUnavailable, got %v", err)
	}
	if err := st.Aggregate(context.Background(), time.Hour); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("noop Aggregate must return ErrStoreUnavailable, got %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("noop Close must not error, got %v", err)
	}
}

// --- DB-backed tests (skipped without a live Postgres) ---
//
// These require FDH_TEST_PG_DSN pointing at a throwaway Postgres. When unset
// they SKIP, so `go test ./internal/portalapi/...` passes in CI without a DB
// while still exercising the real SQL path locally / in an integration job.

func pgStore(t *testing.T) *pgxStore {
	t.Helper()
	dsn := os.Getenv("FDH_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("FDH_TEST_PG_DSN not set; skipping DB-backed telemetry test")
	}
	st, err := Open(context.Background(), dsn, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !st.Available() {
		t.Fatalf("FDH_TEST_PG_DSN set but store is not Available(); check the DSN/DB")
	}
	pg, ok := st.(*pgxStore)
	if !ok {
		t.Fatalf("expected a live *pgxStore, got %T", st)
	}
	// Clean slate so assertions are deterministic.
	if _, err := pg.pool.Exec(context.Background(),
		`TRUNCATE events, agg_component_daily, agg_funnel_daily`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	return pg
}

// TestPG_InsertAndListActivation_RoundTrip is the restart-survives property at
// the store level (§11.3): an activation Insert is durably retrievable via
// ListActivation. Because the row lives in Postgres, a process restart (a fresh
// Open against the same DSN) would still see it — unlike the old ring buffer.
func TestPG_InsertAndListActivation_RoundTrip(t *testing.T) {
	pg := pgStore(t)
	ctx := context.Background()

	ev := Event{
		Event:           "activation",
		Step:            "install-cli",
		WizardSessionID: "sess-123",
		Locale:          "en",
		OS:              "linux",
		Timestamp:       time.Now().UTC(),
	}
	if err := pg.Insert(ctx, ev); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := pg.ListActivation(ctx, 10)
	if err != nil {
		t.Fatalf("ListActivation: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 activation event, got %d", len(got))
	}
	if got[0].Step != "install-cli" || got[0].WizardSessionID != "sess-123" {
		t.Fatalf("round-trip mismatch: %+v", got[0])
	}

	// Simulate a restart: a fresh Open against the same DSN still sees the row.
	reopened, err := Open(ctx, os.Getenv("FDH_TEST_PG_DSN"), nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	after, err := reopened.ListActivation(ctx, 10)
	if err != nil {
		t.Fatalf("ListActivation after reopen: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("activation events must survive a restart; got %d", len(after))
	}
}

// TestPG_Aggregate_RollupAndRetention proves the aggregation loop rolls up
// component counts and prunes raw events past the window while preserving the
// aggregate contribution (design D6).
func TestPG_Aggregate_RollupAndRetention(t *testing.T) {
	pg := pgStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	old := now.Add(-200 * 24 * time.Hour) // older than a 180-day window

	mustInsert := func(e Event) {
		if err := pg.Insert(ctx, e); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}
	// Two installs of the same component (one old, one recent) + one resolve.
	mustInsert(Event{Event: "install", Kind: "skill", Namespace: "forge", Name: "ds", Timestamp: old})
	mustInsert(Event{Event: "install", Kind: "skill", Namespace: "forge", Name: "ds", Timestamp: now})
	mustInsert(Event{Event: "resolve", Kind: "skill", Namespace: "forge", Name: "ds", Timestamp: now})

	if err := pg.Aggregate(ctx, 180*24*time.Hour); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	// The old raw event is pruned; the recent ones remain.
	var rawCount int
	if err := pg.pool.QueryRow(ctx, `SELECT count(*) FROM events`).Scan(&rawCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if rawCount != 2 {
		t.Fatalf("retention should leave 2 recent raw events, got %d", rawCount)
	}

	// The aggregate still reflects BOTH installs (the pruned one's contribution
	// survives in agg_component_daily).
	var aggInstall int
	if err := pg.pool.QueryRow(ctx,
		`SELECT COALESCE(sum(count),0) FROM agg_component_daily WHERE event='install' AND name='ds'`).
		Scan(&aggInstall); err != nil {
		t.Fatalf("sum agg installs: %v", err)
	}
	if aggInstall != 2 {
		t.Fatalf("aggregate install count must include the pruned event; want 2, got %d", aggInstall)
	}
}
