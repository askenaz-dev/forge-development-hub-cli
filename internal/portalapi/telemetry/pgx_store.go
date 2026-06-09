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
	if retention > 0 {
		cutoff := time.Now().UTC().Add(-retention)
		if _, err := tx.Exec(ctx, `DELETE FROM events WHERE ts < $1`, cutoff); err != nil {
			return fmt.Errorf("telemetry retention prune: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("telemetry aggregate commit: %w", err)
	}
	return nil
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
