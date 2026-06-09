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
		`TRUNCATE events, agg_component_daily, agg_funnel_daily, install_claims`); err != nil {
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

// TestPG_AnalyticsReads exercises the Stage-2 aggregate query methods against a
// live DB: TopComponents, Trends, Funnel, SummaryByEvent, and ListFeedback.
func TestPG_AnalyticsReads(t *testing.T) {
	pg := pgStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	must := func(e Event) {
		if err := pg.Insert(ctx, e); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}
	must(Event{Event: "install", Kind: "skill", Namespace: "forge", Name: "ds", Timestamp: now})
	must(Event{Event: "install", Kind: "skill", Namespace: "forge", Name: "ds", Timestamp: now})
	must(Event{Event: "install", Kind: "rule", Namespace: "forge", Name: "lint", Timestamp: now})
	must(Event{Event: "download", Kind: "skill", Namespace: "forge", Name: "ds", Timestamp: now})
	must(Event{Event: "activation", Step: "install-cli", Timestamp: now})
	must(Event{Event: "activation", Step: "doctor-passed", Timestamp: now})
	r := 5
	must(Event{Event: "feedback", Rating: &r, Category: "idea", Text: "love it", Timestamp: now})

	// Roll up so the aggregate-backed reads have data.
	if err := pg.Aggregate(ctx, 0); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	// SummaryByEvent.
	total, byEvent, since, err := pg.SummaryByEvent(ctx)
	if err != nil {
		t.Fatalf("SummaryByEvent: %v", err)
	}
	if total != 7 {
		t.Fatalf("summary total want 7, got %d", total)
	}
	if since.IsZero() {
		t.Fatalf("summary since must be set")
	}
	counts := map[string]int64{}
	for _, ec := range byEvent {
		counts[ec.Event] = ec.Count
	}
	if counts["install"] != 3 || counts["feedback"] != 1 {
		t.Fatalf("summary breakdown wrong: %+v", counts)
	}

	// TopComponents(install): ds (2) before lint (1).
	top, err := pg.TopComponents(ctx, "install", 10)
	if err != nil {
		t.Fatalf("TopComponents: %v", err)
	}
	if len(top) < 2 || top[0].Name != "ds" || top[0].Count != 2 {
		t.Fatalf("top install want ds=2 first, got %+v", top)
	}

	// Trends(install, 7d): one day, count 2.
	trends, err := pg.Trends(ctx, "install", 7)
	if err != nil {
		t.Fatalf("Trends: %v", err)
	}
	var sum int64
	for _, p := range trends {
		sum += p.Count
	}
	if sum != 2 {
		t.Fatalf("install trend sum want 2, got %d (%+v)", sum, trends)
	}

	// Funnel: two steps.
	funnel, err := pg.Funnel(ctx)
	if err != nil {
		t.Fatalf("Funnel: %v", err)
	}
	if len(funnel) != 2 {
		t.Fatalf("funnel want 2 steps, got %d", len(funnel))
	}

	// ListFeedback: one row.
	fb, fbTotal, err := pg.ListFeedback(ctx, 50, 0)
	if err != nil {
		t.Fatalf("ListFeedback: %v", err)
	}
	if fbTotal != 1 || len(fb) != 1 || fb[0].Category != "idea" || fb[0].Rating == nil || *fb[0].Rating != 5 {
		t.Fatalf("feedback round-trip wrong: total=%d rows=%+v", fbTotal, fb)
	}

	// EventCount.
	n, err := pg.EventCount(ctx)
	if err != nil {
		t.Fatalf("EventCount: %v", err)
	}
	if n != 7 {
		t.Fatalf("event count want 7, got %d", n)
	}
}

// TestPG_ClaimIsOnlyIdentityLink is the §12.2 guardrail at the store level: the
// install_id↔identity mapping lives ONLY in the separate install_claims table,
// written ONLY by the explicit Claim, and ActivityFor surfaces an install ONLY
// after such a claim. The events table never gains an identity column.
func TestPG_ClaimIsOnlyIdentityLink(t *testing.T) {
	pg := pgStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// An anonymous install under a pseudonymous id.
	if err := pg.Insert(ctx, Event{
		Event: "install", Kind: "skill", Name: "design-system", Version: "1.2.0",
		InstallID: "machine-xyz", Timestamp: now,
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Before any claim, ActivityFor returns nothing (no de-pseudonymization).
	before, err := pg.ActivityFor(ctx, "dev@example.com", 100)
	if err != nil {
		t.Fatalf("ActivityFor before: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("no activity must appear before a claim, got %d", len(before))
	}

	// The events table holds the row but install_claims is empty: no identity
	// link exists yet.
	var claimRows int
	if err := pg.pool.QueryRow(ctx, `SELECT count(*) FROM install_claims`).Scan(&claimRows); err != nil {
		t.Fatalf("count claims: %v", err)
	}
	if claimRows != 0 {
		t.Fatalf("no claim must exist before an explicit Claim, got %d", claimRows)
	}

	// The explicit, user-initiated claim is the ONLY way the link is created.
	if err := pg.Claim(ctx, "machine-xyz", "dev@example.com"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	// Idempotent re-claim must not duplicate.
	if err := pg.Claim(ctx, "machine-xyz", "dev@example.com"); err != nil {
		t.Fatalf("Claim (re): %v", err)
	}
	if err := pg.pool.QueryRow(ctx, `SELECT count(*) FROM install_claims`).Scan(&claimRows); err != nil {
		t.Fatalf("count claims after: %v", err)
	}
	if claimRows != 1 {
		t.Fatalf("claim must be idempotent (1 row), got %d", claimRows)
	}

	// Now the install surfaces in the user's feed.
	after, err := pg.ActivityFor(ctx, "dev@example.com", 100)
	if err != nil {
		t.Fatalf("ActivityFor after: %v", err)
	}
	if len(after) != 1 || after[0].Name != "design-system" || after[0].Version != "1.2.0" {
		t.Fatalf("activity after claim wrong: %+v", after)
	}

	// The events table schema has no identity column — assert it directly.
	var idCols int
	if err := pg.pool.QueryRow(ctx, `
SELECT count(*) FROM information_schema.columns
WHERE table_name = 'events'
  AND column_name IN ('email','username','user','user_email','hostname','ip','user_id')`).Scan(&idCols); err != nil {
		t.Fatalf("introspect events columns: %v", err)
	}
	if idCols != 0 {
		t.Fatalf("events table must have NO identity column, found %d", idCols)
	}
}
