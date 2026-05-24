# Portal admin guide

For developers with the `fdh-admins` group claim. The admin shell at
`/admin` is the entry point. This page covers what's there and the
backing API operations.

## What admins can do today (MVP)

| Task                                       | UI surface          | API equivalent                        |
| ------------------------------------------ | ------------------- | ------------------------------------- |
| See the portal's view of the registry       | `/admin` overview   | `GET /api/v1/skills`                  |
| Force an immediate registry refresh         | (CLI for now)       | `POST /api/v1/refresh`                |
| Inspect recent activation events            | (CLI for now)       | `GET /api/v1/admin/activation`        |
| See user identity / role mapping             | `/profile`          | `GET /api/v1/auth/me`                 |

The MVP shell exposes role-gated affordances as placeholders; full
inline controls (refresh button, activation log table, role overview)
land with the next OpenSpec changes (`installer-write-flows`, `governance-full`).

## Forcing a registry refresh

The API auto-refreshes every 60 seconds. To pick up a just-pushed
commit without waiting, force a refresh:

```sh
TOKEN=<keycloak-access-token-with-publisher-role>
curl -X POST \
  -H "Authorization: Bearer ${TOKEN}" \
  https://fdh.askenaz.dev/api/v1/refresh
```

The response includes `refreshed_at` and `skill_count`.

## Configuring the role map

Roles flow from Keycloak group claims through a YAML ConfigMap mounted
into the API. Edit the ConfigMap, restart the API pods, and the new map
is in effect.

```yaml
# role-map.yaml
claim: groups
map:
  fdh-admins: admin
  fdh-publishers: publisher
  fdh-reviewers: reviewer
  fdh-authors: author
```

The portal recognizes exactly these roles, in precedence order:

```
anonymous < consumer < author < reviewer < publisher < admin
```

Unmapped authenticated users default to `consumer`. Unmapped anonymous
sessions are `anonymous`.

## Inspecting activation events

The onboarding wizard emits structured log lines for every step
completion + skip. The Go API exposes the most recent events:

```sh
curl -H "Authorization: Bearer ${TOKEN}" \
  https://fdh.askenaz.dev/api/v1/admin/activation
```

The endpoint is admin-only and returns events from an in-memory ring
buffer. Persistent storage + an analytics dashboard land in the
`analytics` change.

## Observability

- **Metrics**: Prometheus scrapes `/metrics` on the API (handled by the
  ServiceMonitor in the Helm chart).
- **Traces**: OTLP export to `${OTEL_EXPORTER_OTLP_ENDPOINT}`.
- **Logs**: stdout JSON. Every request log line includes `trace_id`,
  `user_id`, `route`, `status`, and `latency_ms`.

Key Prometheus metrics:

| Metric                                          | What it tells you                       |
| ----------------------------------------------- | --------------------------------------- |
| `fdh_portal_api_request_duration_seconds`       | Per-route latency histogram             |
| `fdh_portal_api_registry_refresh_total`         | Refresh attempts by result              |
| `fdh_portal_api_registry_refresh_duration_seconds` | Time spent refreshing the cache         |
| `fdh_portal_api_registry_cache_size`            | Number of skills currently cached       |

## Operational runbook

| Symptom                                  | Where to look                                    | Likely cause                                |
| ---------------------------------------- | ------------------------------------------------ | ------------------------------------------- |
| Portal shows old skill list              | `fdh_portal_api_registry_refresh_total{result}`  | Last refresh failed; check API logs         |
| Sign-in loops back to landing            | API logs for `unauthorized`                      | Role-map ConfigMap drift / Keycloak claim missing |
| Admin page shows "no admin group"        | `/api/v1/auth/me` body                           | User's Keycloak group not mapped or missing |
| `fdh install` fails with 401             | API logs for the matching trace_id               | Bearer token missing or wrong audience      |
| `/readyz` returns 503                    | First refresh hasn't completed                   | Registry unreachable or auth misconfigured  |
