## 1. Event ingestion contract (portal)

- [ ] 1.1 Define the versioned event envelope type (`schema_version`, `event_name`, `occurred_at`, optional `install_id`/`wizard_session_id`, constrained `attributes`)
- [ ] 1.2 Implement `POST /api/v1/events`, generalizing `activation.go`; keep `/api/v1/activation` accepting activation events for backward compatibility
- [ ] 1.3 Implement the closed `event_name` taxonomy with per-name attribute validation (drop unknown keys, reject unknown names)
- [ ] 1.4 Make ingestion non-blocking (buffered/async emit; slow exporter never adds request latency or errors)

## 2. Tier 0 server-side derivation

- [ ] 2.1 Add a labeled download counter on the wire bundle route in `metrics.go` and emit `bundle.downloaded`
- [ ] 2.2 Emit `search.zero_results` from the catalog search path with the normalized query
- [ ] 2.3 Emit `component.not_found` from the component 404 paths
- [ ] 2.4 Add browse→detail→install funnel attributes to the relevant handlers/web events

## 3. Privacy posture (portal)

- [ ] 3.1 Add the `telemetry` config block (`mode`, `ip_handling`, `identity`, `retention_days`, `otlp_endpoint`) to `config.go` with internal/public defaults
- [ ] 3.2 Apply `ip_handling` in `withRequestLogging` and in event attachment
- [ ] 3.3 Enforce `anonymous_first`: never persist `user_id` on events or join it to `install_id`
- [ ] 3.4 Enforce the content-exclusion guarantee at the ingestion boundary
- [ ] 3.5 Implement retention enforcement (records past `retention_days` deleted)

## 4. OpenTelemetry export (portal)

- [ ] 4.1 Add the OTel Go SDK + OTLP exporter dependency and wire SDK init in `server.go`
- [ ] 4.2 Bridge structured `slog` logs and product events to OTLP log records with `event.name`
- [ ] 4.3 Carry inbound `traceparent` into exported traces; reconcile existing Prometheus metrics into the OTLP pipeline
- [ ] 4.4 Make export degradable (Collector down → keep serving, buffer/drop, no user-visible error)

## 5. CLI telemetry

- [ ] 5.1 Add anonymous `install_id` (random UUID, no PII) to CLI config with a regenerate path
- [ ] 5.2 Emit `component.installed` / `.uninstalled` / `.updated` from `install.go`, `uninstall.go`, `update_*.go`
- [ ] 5.3 Emit `install.failed` with `error_class` enum; ensure no payload/argv/file contents leave the machine
- [ ] 5.4 Add `fdh config telemetry on|off`, honor `DO_NOT_TRACK`, print one-time first-run notice
- [ ] 5.5 Make emit non-blocking and failure-silent (no command delay or error on portal unreachable)

## 6. Tier 2 voluntary feedback

- [ ] 6.1 Accept `feedback.submitted` (👍/👎 + optional text) at `/api/v1/events`
- [ ] 6.2 Add the feedback UI to the component detail page (`web/components/component-detail.tsx`)
- [ ] 6.3 Add a `fdh feedback` command

## 7. Web instrumentation + insights

- [ ] 7.1 Point web event posting at `/api/v1/events` (BFF; known origin only)
- [ ] 7.2 Build the admin insights view (downloads, demand gaps, funnel, churn, feedback) under `web/app/[locale]/admin/`

## 8. Infrastructure

- [ ] 8.1 Add an OpenTelemetry Collector to the deploy (Helm/compose) with the ELK exporter and `event.*` routing
- [ ] 8.2 Provision the ELK (or chosen) store; document adding a columnar exporter later as a Collector-only change
- [ ] 8.3 Document the internal vs public telemetry config and the privacy guarantees

## 9. Validation

- [ ] 9.1 Tests: envelope validation, taxonomy acceptance/rejection, Tier 0 emission, non-blocking ingestion
- [ ] 9.2 Tests: privacy modes (IP handling, anonymous-first, content exclusion, retention)
- [ ] 9.3 Tests: CLI opt-out, `DO_NOT_TRACK`, payload exclusion, failure-silent emit
- [ ] 9.4 `openspec validate add-usage-telemetry --strict` passes
