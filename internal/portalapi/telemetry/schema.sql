-- Telemetry store schema (capability hub-usage-telemetry, design D1/D4/D6).
--
-- This schema is APPLIED IDEMPOTENTLY on Store.Open against a live connection
-- (every statement is CREATE ... IF NOT EXISTS), so there is no separate
-- migration tool — the store self-migrates at boot. Keep every change additive
-- and idempotent.
--
-- PRIVACY INVARIANT (design D4): NO identity / PII column may ever be added to
-- this schema. There is intentionally no username, email, hostname, ip, repo
-- path, or file-content column. The only id is the pseudonymous, rotating,
-- salted `install_id` which is NOT reversible to an identity. The platform
-- stores no install_id -> identity mapping here (a voluntary profile claim,
-- Phase-2 Stage-2 / D5, lives elsewhere and is user-initiated).

-- Raw events. One row per ingested telemetry/activation/feedback event.
-- Bounded retention (design D6, default 180 days) prunes rows from this table;
-- the aggregate tables below are long-lived.
CREATE TABLE IF NOT EXISTS events (
    id                BIGSERIAL PRIMARY KEY,
    event             TEXT        NOT NULL,
    kind              TEXT,
    namespace         TEXT,
    name              TEXT,
    version           TEXT,
    content_hash      TEXT,
    scope             TEXT,
    registry          TEXT,
    os                TEXT,
    locale            TEXT,
    install_id        TEXT,
    step              TEXT,
    wizard_session_id TEXT,
    rating            INTEGER,
    category          TEXT,
    text              TEXT,
    ts                TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Retention prune + activation read both scan by time; index ts.
CREATE INDEX IF NOT EXISTS events_ts_idx ON events (ts);
-- Activation reads filter event='activation' newest-first.
CREATE INDEX IF NOT EXISTS events_event_ts_idx ON events (event, ts);

-- Long-lived rollups (design D6). Populated by the in-process aggregation loop
-- (lowest-ordinal replica) and read by the Stage-2 admin analytics endpoints.
-- Created now so the loop and Stage-2 reads have a stable target; Stage-1 only
-- writes the retention prune + (optionally) refreshes these.

-- Per-component install/download/resolve counts, bucketed by UTC day.
CREATE TABLE IF NOT EXISTS agg_component_daily (
    day          DATE NOT NULL,
    event        TEXT NOT NULL,
    kind         TEXT NOT NULL DEFAULT '',
    namespace    TEXT NOT NULL DEFAULT '',
    name         TEXT NOT NULL DEFAULT '',
    count        BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (day, event, kind, namespace, name)
);

-- Onboarding funnel: count of activation events per wizard step, by UTC day.
CREATE TABLE IF NOT EXISTS agg_funnel_daily (
    day    DATE NOT NULL,
    step   TEXT NOT NULL DEFAULT '',
    count  BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (day, step)
);

-- Voluntary install claims (capability hub-usage-telemetry, design D5 / Stage 2).
--
-- This table is the ONE — and ONLY — identity ↔ telemetry link the platform
-- stores, and it exists EXCLUSIVELY because the signed-in user VOLUNTARILY
-- claimed a machine's pseudonymous install_id into their profile (the explicit
-- user-initiated claim of D5 / task 12.2). It is deliberately SEPARATE from the
-- `events` table: events stay PII-free and are never joined to identity. The
-- platform never derives this mapping by reversing an install_id — it is only
-- ever written by the explicit POST /api/v1/admin/activity/claim flow.
--
-- The user_email here is the signed-in user's own email (the stable key,
-- consistent with the Phase-1 contributions derivation). A user may claim
-- multiple machines (install_ids); the same (install_id, user_email) pair is
-- idempotent (re-claiming is a no-op upsert).
CREATE TABLE IF NOT EXISTS install_claims (
    install_id  TEXT        NOT NULL,
    user_email  TEXT        NOT NULL,
    ts          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (install_id, user_email)
);

-- Activity-feed reads filter claims by the signed-in user's email.
CREATE INDEX IF NOT EXISTS install_claims_user_idx ON install_claims (user_email);
