## Context

The portal API already observes most of what we want, server-side, with no
client telemetry:

- `withRequestLogging` (`internal/portalapi/middleware.go:14`) logs every request
  with route, status, latency, `user_agent`, `user_id`, remote address and trace
  id. Because every `fdh install` is a GET on a wire bundle endpoint
  (`server.go:124`), **downloads are already an observable server event.**
- A Prometheus registry (`internal/portalapi/metrics.go`) exposes operational
  metrics at `/metrics`.
- `activation.go` already accepts frontend-posted events at `/api/v1/activation`
  into an in-memory ring buffer, with a comment naming structured logs as the
  durable record and OTel/OTLP export as a planned milestone (`middleware.go:58`).

The gap is that this signal is unstructured, not aggregated, not exported, and
the CLI emits nothing. The competing "abusive" pattern closes a similar gap by
harvesting the *client* (prompts, whole-session activity). We deliberately do not:
the artifact lifecycle is observable without observing the developer's content.

## Goals / Non-Goals

**Goals:**
- Automated metrics for downloads, demand (zero-result searches), broken
  references (404s), and the browse→detail→install funnel — primarily from
  existing server-side signal.
- A thin, anonymous, opt-out CLI lifecycle signal for churn and install failures
  that the server cannot see.
- An optional voluntary-feedback channel as the only source of *why*.
- Vendor-portable export: instrument once (OTLP), switch backends in the
  Collector, never in the app.
- A single privacy posture switch (internal vs public) configurable per
  deployment.
- A stable transport contract (`POST /api/v1/events` + event envelope) so the
  ingestion backend can later be split out without changing any client.

**Non-Goals:**
- **Invocation tracking.** "Usage" is install + survival + version-adoption, never
  "did the component run." Chasing invocation requires instrumenting the
  developer's harness/session and is the line we do not cross.
- Capturing prompts, file contents, or command arguments beyond the component
  coordinate.
- Building a separate analytics frontend. Insights surface in the existing portal
  admin UI.
- Standing up the separate analytics backend now. We design the seam and defer
  the split.

## Decisions

### D1 — Extend the portal (Option A), design the seam to allow a later split (Option B)

Ingestion lives in the portal API today: generalize `/api/v1/activation` into a
typed `POST /api/v1/events`. The thing we must not break later is the **transport
contract** (endpoint + envelope). Where the bytes ultimately land (in-process
handler now, dedicated service later) is an internal detail. Migrating A→B must
require **zero** changes to the web or CLI.

### D2 — BFF for every client, including the CLI

The web frontend posts only to the portal (a known origin), never to an analytics
backend. We extend the same rule to the CLI: it posts lifecycle events to the
**same** `/api/v1/events` endpoint. This yields one public ingress and one OTLP
egress inside the trust boundary. After a future B split, the portal still fronts
the analytics service as BFF; clients are unaffected.

```
  Browser (Next.js)              CLI (fdh)
        │  POST /api/v1/events        │  POST /api/v1/events
        ▼                             ▼
   ┌──────────────────────────────────────────┐
   │   Portal API (Go) = BFF + único ingreso   │
   │   · request logs → downloads, 404, search │  Tier 0 (already flows)
   │   · /api/v1/events → typed sink           │  Tier 1 / Tier 2
   │   · OTel SDK → OTLP exporter              │
   └───────────────────┬──────────────────────┘
                        │ OTLP (inside trust boundary)
                        ▼
            ┌────────────────────────┐
            │  OpenTelemetry Collector │  ◀── vendor-portability seam
            └───┬──────────┬───────────┘
                ▼          ▼
          Elastic/ELK   (future) Datadog · Loki · ClickHouse
```

### D3 — Three tiers, mapped to the cheapest source

- **Tier 0 (server, passive):** downloads, zero-result searches, 404s, funnel —
  derived from request logs + a download counter. No client code.
- **Tier 1 (CLI, lifecycle):** `installed` / `uninstalled` / `updated` /
  `install.failed`. Anonymous, opt-out, payloadless beyond coordinate + outcome.
- **Tier 2 (voluntary):** 👍/👎 + optional text. The only source of *why*. Kept
  minimal but not dropped, because "what to improve" needs causality.

### D4 — Structured outcome, never payload (Tier 1)

Lifecycle events carry `{kind, namespace, name, version, os, cli_version, scope,
agent, install_id}` and, for failures, an `error_class` enum
(`signature_mismatch | network | disk | permission | other`). No prompts, no file
contents, no raw argv. `error_class` is what makes failures actionable without
surveillance.

### D5 — Anonymous installation id

A random UUID generated on first run and stored in CLI config — derived from no
PII (no hostname, MAC, or username). Stable enough to measure churn/retention over
a window; user-regenerable; in `public` mode the server never joins it to an
authenticated `user_id`.

### D6 — Product events as OTel log records

Model Tier 0/1/2 events as OTLP **log records** with a well-known `event.name`
attribute (`component.installed`, `search.zero_results`, `feedback.submitted`),
rather than waiting on a bespoke events API. The Collector routes `event.*`
records to the analytics store and ops signals to traces/metrics backends. ELK
first; a columnar store (ClickHouse) can be added later as another exporter for
heavy product aggregations — an additive Collector change.

### D7 — Privacy posture as one config switch

A `telemetry` block selects `mode: internal | public`, which sets defaults for
`ip_handling` (full/truncate/hash/drop), `identity` (attributed/anonymous_first),
and `retention_days`. Public launch defaults to truncated IP + anonymous-first +
short retention; internal deployments may retain `user_id`/IP under an explicit
policy. Fits the existing `config.go` pattern.

## Risks / Trade-offs

- **Write-load coupling (A).** Event ingestion shares the process serving the
  catalog. Acceptable at pilot scale; the D1 seam lets us peel ingestion out
  before it matters. Mitigate with non-blocking/buffered emit so a slow exporter
  never stalls a request.
- **New infra to operate.** Collector + ELK is real operational surface, on a
  cluster that has had origin-reachability issues. Mitigate by making telemetry
  fully degradable: if the Collector is down, the portal keeps serving and drops
  or buffers events; export failures are never user-visible.
- **Opt-out vs consent (public).** Opt-out anonymous lifecycle events match the
  industry norm (Homebrew, Next.js, .NET CLI) and the "automated, not voluntary"
  goal, but a public audience raises expectations. Mitigate with a first-run
  notice, `fdh config telemetry off`, and honoring `DO_NOT_TRACK`.
- **Retention vs anonymity tension.** A stable `install_id` enables churn metrics
  but is a pseudonymous identifier. Mitigate via D5 (no PII derivation, user
  regeneration, no join to `user_id` in public mode) and bounded retention.
- **IP as PII in public mode.** Request logs currently store full IP + user_id
  (`middleware.go:29`). Public mode must truncate/hash IP and set retention; this
  is a behavior change to existing logging, covered by `telemetry-privacy`.
- **Install ≠ retention.** We intentionally approximate retention via uninstall
  events and version-adoption rather than an inventory heartbeat. If that proves
  insufficient, an opt-out inventory heartbeat (Forge-managed components only) is
  a later, explicitly-flagged addition — out of scope for v1.
