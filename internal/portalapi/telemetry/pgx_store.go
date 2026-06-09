package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxStore is the live Postgres-backed Store. The shared instance is reachable
// by every API replica (design D1) so aggregation across replicas is correct.
type pgxStore struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// nullable maps an empty string to a typed SQL NULL so we never persist ""
// where a column is semantically absent. Keeps the rows clean for aggregation.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Insert persists one event. The column list is the closed schema; there is no
// path here to write any identity/PII column because none exists. ts defaults
// to the client timestamp when provided, else server now().
func (s *pgxStore) Insert(ctx context.Context, e Event) error {
	ts := e.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	const q = `
INSERT INTO events
  (event, kind, namespace, name, version, content_hash, scope, registry,
   os, locale, install_id, step, wizard_session_id, rating, category, text, ts)
VALUES
  ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`
	var rating any
	if e.Rating != nil {
		rating = *e.Rating
	}
	_, err := s.pool.Exec(ctx, q,
		e.Event,
		nullable(e.Kind),
		nullable(e.Namespace),
		nullable(e.Name),
		nullable(e.Version),
		nullable(e.ContentHash),
		nullable(e.Scope),
		nullable(e.Registry),
		nullable(e.OS),
		nullable(e.Locale),
		nullable(e.InstallID),
		nullable(e.Step),
		nullable(e.WizardSessionID),
		rating,
		nullable(e.Category),
		nullable(e.Text),
		ts.UTC(),
	)
	if err != nil {
		return fmt.Errorf("telemetry insert: %w", err)
	}
	return nil
}

// ListActivation returns the most recent activation events newest-first. It
// replaces the in-memory activationRing snapshot: durable, survives restarts.
func (s *pgxStore) ListActivation(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 512
	}
	const q = `
SELECT event, step, wizard_session_id, locale, os, ts
FROM events
WHERE event = 'activation'
ORDER BY ts DESC, id DESC
LIMIT $1`
	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("telemetry list activation: %w", err)
	}
	defer rows.Close()

	out := make([]Event, 0, limit)
	for rows.Next() {
		var (
			ev                     Event
			step, wsid, locale, os *string
			ts                     time.Time
		)
		if err := rows.Scan(&ev.Event, &step, &wsid, &locale, &os, &ts); err != nil {
			return nil, fmt.Errorf("telemetry scan activation: %w", err)
		}
		ev.Step = deref(step)
		ev.WizardSessionID = deref(wsid)
		ev.Locale = deref(locale)
		ev.OS = deref(os)
		ev.Timestamp = ts.UTC()
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("telemetry rows activation: %w", err)
	}
	return out, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Aggregate recomputes the long-lived rollups from raw events, then prunes raw
// events older than retention. Aggregation is idempotent (upsert by key) so a
// re-run over an overlapping window self-corrects. It runs inside one
// transaction so a crash mid-pass leaves either the prior or the new state, and
// the retention prune never deletes rows whose contribution was not yet rolled
// up. Driven by the in-process loop on the elected (lowest-ordinal) replica.
func (s *pgxStore) Aggregate(ctx context.Context, retention time.Duration) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("telemetry aggregate begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Component install/download/resolve counts per UTC day. Full recompute by
	// upsert: deterministic and idempotent for the scale involved (design D6).
	const aggComponent = `
INSERT INTO agg_component_daily (day, event, kind, namespace, name, count)
SELECT (ts AT TIME ZONE 'UTC')::date AS day,
       event,
       COALESCE(kind, '')      AS kind,
       COALESCE(namespace, '') AS namespace,
       COALESCE(name, '')      AS name,
       count(*)                AS count
FROM events
WHERE event IN ('install', 'download', 'resolve')
GROUP BY day, event, kind, namespace, name
ON CONFLICT (day, event, kind, namespace, name)
DO UPDATE SET count = EXCLUDED.count`
	if _, err := tx.Exec(ctx, aggComponent); err != nil {
		return fmt.Errorf("telemetry aggregate components: %w", err)
	}

	// Onboarding funnel: activation steps per UTC day.
	const aggFunnel = `
INSERT INTO agg_funnel_daily (day, step, count)
SELECT (ts AT TIME ZONE 'UTC')::date AS day,
       COALESCE(step, '') AS step,
       count(*)           AS count
FROM events
WHERE event = 'activation'
GROUP BY day, step
ON CONFLICT (day, step)
DO UPDATE SET count = EXCLUDED.count`
	if _, err := tx.Exec(ctx, aggFunnel); err != nil {
		return fmt.Errorf("telemetry aggregate funnel: %w", err)
	}

	// Retention prune: drop raw events past the window. Aggregates above were
	// recomputed first this same transaction, so their contribution survives.
	//
	// Floor the cutoff to the start of its UTC day so a calendar day is either
	// fully retained or fully pruned — never half-deleted. A half-pruned day
	// would still emit a GROUP BY row on the next pass and the full-recompute
	// upsert (DO UPDATE SET count = EXCLUDED.count) would overwrite the correct
	// rollup with a count over the shrunken window, dragging the long-lived
	// aggregate down. Flooring keeps the prune and the recompute consistent: a
	// day is aggregated at full count on the last pass before it is pruned, then
	// never re-aggregated over a partial window.
	if retention > 0 {
		cutoff := time.Now().UTC().Add(-retention)
		cutoffDay := time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(), 0, 0, 0, 0, time.UTC)
		if _, err := tx.Exec(ctx, `DELETE FROM events WHERE ts < $1`, cutoffDay); err != nil {
			return fmt.Errorf("telemetry retention prune: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("telemetry aggregate commit: %w", err)
	}
	return nil
}

// --- Stage-2 admin analytics reads (aggregates only, no identity join) ---

// SummaryByEvent returns the total retained-event count and the per-event-type
// breakdown, plus the earliest retained event timestamp. It reads the raw
// `events` table (bounded by retention) so the "by event" breakdown reflects
// the live, retained window. AGGREGATE only — no install_id or identity column
// is selected.
func (s *pgxStore) SummaryByEvent(ctx context.Context) (int64, []EventCount, time.Time, error) {
	const q = `
SELECT event, count(*) AS c, min(ts) AS since
FROM events
GROUP BY event`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return 0, nil, time.Time{}, fmt.Errorf("telemetry summary: %w", err)
	}
	defer rows.Close()

	var (
		total   int64
		byEvent []EventCount
		since   time.Time
	)
	for rows.Next() {
		var (
			ev      string
			c       int64
			eventTS time.Time
		)
		if err := rows.Scan(&ev, &c, &eventTS); err != nil {
			return 0, nil, time.Time{}, fmt.Errorf("telemetry summary scan: %w", err)
		}
		total += c
		byEvent = append(byEvent, EventCount{Event: ev, Count: c})
		if since.IsZero() || eventTS.Before(since) {
			since = eventTS.UTC()
		}
	}
	if err := rows.Err(); err != nil {
		return 0, nil, time.Time{}, fmt.Errorf("telemetry summary rows: %w", err)
	}
	return total, byEvent, since, nil
}

// TopComponents returns the most-counted components for metric (install|
// download), highest first, from the long-lived agg_component_daily rollup so
// the figures survive raw-event retention pruning (design D6). Aggregate only.
func (s *pgxStore) TopComponents(ctx context.Context, metric string, limit int) ([]ComponentCount, error) {
	if metric != "install" && metric != "download" {
		metric = "install"
	}
	if limit <= 0 || limit > 200 {
		limit = 10
	}
	const q = `
SELECT kind, namespace, name, COALESCE(sum(count),0) AS total
FROM agg_component_daily
WHERE event = $1
GROUP BY kind, namespace, name
ORDER BY total DESC, kind, namespace, name
LIMIT $2`
	rows, err := s.pool.Query(ctx, q, metric, limit)
	if err != nil {
		return nil, fmt.Errorf("telemetry top components: %w", err)
	}
	defer rows.Close()

	out := make([]ComponentCount, 0, limit)
	for rows.Next() {
		var cc ComponentCount
		if err := rows.Scan(&cc.Kind, &cc.Namespace, &cc.Name, &cc.Count); err != nil {
			return nil, fmt.Errorf("telemetry top components scan: %w", err)
		}
		out = append(out, cc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("telemetry top components rows: %w", err)
	}
	return out, nil
}

// Trends returns per-day counts for event over the last `days` days (UTC),
// oldest first, from agg_component_daily (for install/download/resolve) or
// agg_funnel_daily (for activation). Aggregate only.
func (s *pgxStore) Trends(ctx context.Context, event string, days int) ([]TrendPoint, error) {
	if days <= 0 || days > 365 {
		days = 30
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days)

	var (
		q    string
		args []any
	)
	switch event {
	case "activation":
		// activation rolls up in agg_funnel_daily and takes only the cutoff —
		// pass a single arg so the SQL has no unused placeholder (a $1-unused
		// query is fragile across pgx exec modes).
		q = `
SELECT day, COALESCE(sum(count),0) AS total
FROM agg_funnel_daily
WHERE day >= $1::date
GROUP BY day
ORDER BY day ASC`
		args = []any{cutoff}
	default:
		q = `
SELECT day, COALESCE(sum(count),0) AS total
FROM agg_component_daily
WHERE event = $1 AND day >= $2::date
GROUP BY day
ORDER BY day ASC`
		args = []any{event, cutoff}
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("telemetry trends: %w", err)
	}
	defer rows.Close()

	out := make([]TrendPoint, 0, days)
	for rows.Next() {
		var (
			day time.Time
			c   int64
		)
		if err := rows.Scan(&day, &c); err != nil {
			return nil, fmt.Errorf("telemetry trends scan: %w", err)
		}
		out = append(out, TrendPoint{Date: day.UTC().Format("2006-01-02"), Count: c})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("telemetry trends rows: %w", err)
	}
	return out, nil
}

// Funnel returns the onboarding-funnel step counts aggregated across all days,
// highest first, from the long-lived agg_funnel_daily rollup. Aggregate only.
func (s *pgxStore) Funnel(ctx context.Context) ([]FunnelStep, error) {
	const q = `
SELECT step, COALESCE(sum(count),0) AS total
FROM agg_funnel_daily
GROUP BY step
ORDER BY total DESC, step`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("telemetry funnel: %w", err)
	}
	defer rows.Close()

	out := make([]FunnelStep, 0, 8)
	for rows.Next() {
		var fs FunnelStep
		if err := rows.Scan(&fs.Step, &fs.Count); err != nil {
			return nil, fmt.Errorf("telemetry funnel scan: %w", err)
		}
		out = append(out, fs)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("telemetry funnel rows: %w", err)
	}
	return out, nil
}

// EventCount returns the number of raw events currently retained.
func (s *pgxStore) EventCount(ctx context.Context) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM events`).Scan(&n); err != nil {
		return 0, fmt.Errorf("telemetry event count: %w", err)
	}
	return n, nil
}

// --- Stage-2 feedback reads ---

// ListFeedback returns feedback events (newest first), paginated by
// limit/offset, plus the total count. Feedback rows carry only the structured
// rating/category and the free text — no identity (design D8).
func (s *pgxStore) ListFeedback(ctx context.Context, limit, offset int) ([]Event, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	var total int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM events WHERE event = 'feedback'`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("telemetry feedback count: %w", err)
	}

	const q = `
SELECT rating, category, text, ts
FROM events
WHERE event = 'feedback'
ORDER BY ts DESC, id DESC
LIMIT $1 OFFSET $2`
	rows, err := s.pool.Query(ctx, q, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("telemetry feedback list: %w", err)
	}
	defer rows.Close()

	out := make([]Event, 0, limit)
	for rows.Next() {
		var (
			ev       Event
			rating   *int
			category *string
			text     *string
			ts       time.Time
		)
		if err := rows.Scan(&rating, &category, &text, &ts); err != nil {
			return nil, 0, fmt.Errorf("telemetry feedback scan: %w", err)
		}
		ev.Event = "feedback"
		ev.Rating = rating
		ev.Category = deref(category)
		ev.Text = deref(text)
		ev.Timestamp = ts.UTC()
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("telemetry feedback rows: %w", err)
	}
	return out, total, nil
}

// --- Stage-2 profile activity: the ONLY identity↔telemetry link (D5) ---

// Claim links a pseudonymous install_id to the signed-in user's email,
// idempotently (re-claiming the same pair updates the timestamp). This is the
// single, explicit, user-initiated mapping; it lives in the SEPARATE
// install_claims table and is NEVER joined back into the events PII-free schema
// except through the user's own ActivityFor read.
func (s *pgxStore) Claim(ctx context.Context, installID, userEmail string) error {
	const q = `
INSERT INTO install_claims (install_id, user_email, ts)
VALUES ($1, $2, now())
ON CONFLICT (install_id, user_email) DO UPDATE SET ts = now()`
	if _, err := s.pool.Exec(ctx, q, installID, userEmail); err != nil {
		return fmt.Errorf("telemetry claim: %w", err)
	}
	return nil
}

// ActivityFor returns the installs the user voluntarily claimed, newest first.
// It joins the user's claimed install_ids (install_claims) to install events
// for those ids — so install activity appears ONLY for an install_id the user
// explicitly claimed, never by reversing an unclaimed pseudonymous id (D5).
func (s *pgxStore) ActivityFor(ctx context.Context, userEmail string, limit int) ([]ClaimedInstall, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
SELECT e.kind, e.name, e.version, e.ts
FROM events e
JOIN install_claims c ON c.install_id = e.install_id
WHERE c.user_email = $1 AND e.event = 'install'
ORDER BY e.ts DESC, e.id DESC
LIMIT $2`
	rows, err := s.pool.Query(ctx, q, userEmail, limit)
	if err != nil {
		return nil, fmt.Errorf("telemetry activity: %w", err)
	}
	defer rows.Close()

	out := make([]ClaimedInstall, 0, limit)
	for rows.Next() {
		var (
			ci               ClaimedInstall
			kind, name, vers *string
			ts               time.Time
		)
		if err := rows.Scan(&kind, &name, &vers, &ts); err != nil {
			return nil, fmt.Errorf("telemetry activity scan: %w", err)
		}
		ci.Kind = deref(kind)
		ci.Name = deref(name)
		ci.Version = deref(vers)
		ci.Timestamp = ts.UTC()
		out = append(out, ci)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("telemetry activity rows: %w", err)
	}
	return out, nil
}

func (s *pgxStore) Available() bool { return true }

func (s *pgxStore) Close() error {
	s.pool.Close()
	return nil
}

// compile-time assertions.
var (
	_ Store = (*pgxStore)(nil)
	_ Store = (*noopStore)(nil)
)
