## Why

The hub now serves a real public catalog, but the team has no automated read on
adoption, demand, or what to improve. Today the only durable signal is
unstructured request logging (`internal/portalapi/middleware.go`) plus an
in-memory activation ring buffer that `internal/portalapi/activation.go`
explicitly scopes as "for debugging during the pilot." Deciding what to build or
fix next therefore depends on voluntary feedback, which is sparse and biased.

We need automated usage and download metrics, derived primarily from signal that
is **already server-observable**, plus a thin lifecycle signal from the CLI and
an optional voluntary-feedback channel — **without** adopting the surveillance
pattern (harvesting developer prompts, or shipping consolidated session/day
activity reports) that competing systems use. The value we want to measure lives
at the distribution point (the registry), which is server-side; it does not
require observing the developer's content.

## What Changes

- Introduce a typed event ingestion endpoint `POST /api/v1/events` on the portal
  API, shared by the web frontend **and** the CLI. Per the BFF rule, clients
  always post to the portal (a known origin); they never address an analytics
  backend or the OTLP collector directly. This generalizes the existing
  `/api/v1/activation` sink.
- Derive **Tier 0** metrics (bundle downloads, zero-result searches, component
  404s, catalog funnel) from the existing server-side request log and a new
  download counter — no client code, no privacy surface beyond what already
  exists.
- Add **Tier 1** CLI lifecycle events (`installed`, `uninstalled`, `updated`,
  `install.failed`) — anonymous, opt-out, payloadless beyond the component
  coordinate plus a structured outcome (`error_class`, `scope`, `agent`).
- Add **Tier 2** voluntary feedback (👍/👎 + optional free text) on the component
  detail page and via a `fdh feedback` command.
- Instrument the portal with the OpenTelemetry SDK exporting **OTLP** to an
  OpenTelemetry Collector; the Collector fans out to ELK initially and to any
  future backend (Datadog, Loki, ClickHouse) without application changes.
- Add a `telemetry` configuration block selecting an **internal** vs **public**
  privacy posture (IP handling, identity, retention), configurable per
  deployment.
- Define "usage" as **install + survival + version-adoption**, explicitly **not**
  invocation.

## Capabilities

### New Capabilities
- `usage-telemetry`: server-side event ingestion contract (`POST /api/v1/events`),
  the shared event envelope, the Tier 0/1/2 event taxonomy, and Tier 0 derivation
  from request logs.
- `cli-telemetry`: CLI lifecycle event emission, opt-out semantics, `DO_NOT_TRACK`
  support, and the anonymous installation identifier.
- `telemetry-privacy`: internal vs public privacy modes, IP/identity handling,
  retention policy, and the hard content-exclusion guardrails.
- `observability-export`: OpenTelemetry SDK + OTLP + Collector seam for
  vendor-portable export of traces, metrics, logs, and product events.

### Modified Capabilities
<!-- No existing spec files under openspec/specs/ yet; the activation endpoint
     generalization is an implementation change, not a requirement change to a
     published spec. -->

## Impact

- `internal/portalapi/`: new events handler (generalizing `activation.go`), OTel
  instrumentation wired through `server.go` / `middleware.go`, a `telemetry`
  block in `config.go`, and a download counter in `metrics.go`.
- `internal/cli/`: lifecycle event emission in `install.go`, `uninstall.go`,
  `update_*.go`; new `fdh feedback` and `fdh config telemetry` commands; an
  anonymous installation id persisted in CLI config (`config.go` / `context.go`).
- `web/`: event posting to `/api/v1/events`, feedback UI on the component detail
  page (`web/components/component-detail.tsx`), and an insights view under the
  existing admin surface (`web/app/[locale]/admin/`).
- Infra: new OpenTelemetry Collector + a log/analytics store (ELK to start). New
  OTLP egress inside the trust boundary; no new public ingress beyond the portal.
- Dependencies: OpenTelemetry Go SDK + OTLP exporter.
