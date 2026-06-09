# fdh-portal Helm chart

Deploys the FDH portal (API + Web) onto Kubernetes, fronted by a single
Ingress at the configured host. Keycloak is consumed, not provisioned —
this chart expects an existing Keycloak (default: `keycloak.askenaz.dev`).

## Quick start

```sh
# 1. Create the OIDC secret (client_secret from Keycloak + a 32+ byte
#    AUTH_SECRET for Auth.js).
kubectl create secret generic fdh-portal-oidc \
  --from-literal=client_secret=<from-keycloak> \
  --from-literal=auth_secret=$(openssl rand -hex 32) \
  --namespace fdh

# 2. Create the role-map ConfigMap.
kubectl create configmap fdh-portal-role-map \
  --from-file=role-map.yaml=./role-map.yaml \
  --namespace fdh

# 3. Create the TLS secret. With a Cloudflare Origin Certificate
#    (recommended), generate once in the Cloudflare dashboard then:
kubectl create secret tls cloudflare-origin-tls \
  --cert=origin.pem --key=origin.key --namespace fdh

# 4. Install the chart with the defaults already targeted at askenaz.dev.
helm upgrade --install fdh-portal ./deploy/helm/fdh-portal \
  --namespace fdh --create-namespace
```

## Values

See `values.yaml` for the full surface. Key knobs:

| Path                          | Default                                              | Purpose                                                  |
| ----------------------------- | ---------------------------------------------------- | -------------------------------------------------------- |
| `api.image.tag`               | `""` (chart appVersion)                              | Override the API image tag.                              |
| `web.image.tag`               | `""` (chart appVersion)                              | Override the web image tag.                              |
| `portal.registryURL`          | `https://github.com/askenaz-dev/forge-development-hub.git` | Git URL of the production skill registry.                |
| `oidc.discoveryURL`           | `https://keycloak.askenaz.dev/realms/askenaz`        | Keycloak realm discovery URL.                            |
| `oidc.clientSecretRef`        | `fdh-portal-oidc` / `client_secret`                  | Reference to the K8s secret holding the client secret.   |
| `ingress.host`                | `fdh.askenaz.dev`                                    | Public hostname.                                         |
| `ingress.tls.secretName`      | `cloudflare-origin-tls`                              | TLS secret (Origin Cert recommended).                    |
| `observability.otel.endpoint` | `""`                                                 | OTLP collector for trace export.                         |
| `api.persistence.size`        | `1Gi`                                                | Size of each API pod's hub-clone PVC (StatefulSet).      |
| `api.persistence.storageClassName` | `""` (cluster default)                          | StorageClass for the API PVC; empty uses the default.    |
| `telemetry.store.enabled`     | `false`                                              | Opt-in: render the in-cluster Postgres telemetry store + Secret and wire `FDH_TELEMETRY_DSN` into the API (NON-FATAL when unreachable). |
| `telemetry.store.existingSecret` | `""`                                              | Use a pre-existing Secret (`POSTGRES_PASSWORD` + `FDH_TELEMETRY_DSN`), e.g. a managed-Postgres DSN; the chart then creates no Secret. |
| `telemetry.store.persistence.size` | `2Gi`                                           | Size of the Postgres PVC (StatefulSet).                  |
| `gitops.enabled`              | `false`                                              | Opt-in: wire the GitHub App ("the bot") credential into the API so the web → Git CONFIG write surface lights up (portal-gitops-write). Off -> no `GITHUB_APP_*` env, no Secret; the API boots and serves reads, write endpoints return 503 `gitops_not_configured` (SHIPS DARK / NON-FATAL). |
| `gitops.existingSecret`       | `""`                                                 | Use a pre-existing Secret holding the App id / installation id / private key (production path; the chart then creates no Secret). Empty -> the chart renders a Secret from the inline `gitops.appId`/`installationID`/`privateKey` literals (non-prod / dummy). |
| `gitops.hub.owner` / `gitops.hub.repo` | `askenaz-dev` / `forge-development-hub`       | The single hub repo the App is installed on (single-repo scope).         |

## Upgrading: Deployment → StatefulSet (chart v0.2.0)

As of chart v0.2.0 the API runs as a **StatefulSet** with a per-pod
PersistentVolumeClaim (`registry-data`, RWO) holding the hub clone, so a pod
comes up Ready on restart without re-cloning. Kubernetes cannot change a
resource's `kind` in place, so when upgrading from a chart that deployed the
API as a Deployment, **delete the old Deployment first**:

```sh
kubectl -n fdh delete deployment fdh-portal-fdh-portal-api
helm upgrade fdh-portal ./deploy/helm/fdh-portal -n fdh --reuse-values \
  --set api.image.tag=<current-sha>
```

The brief API gap is masked by the cached web landing. Rollback with
`helm rollback fdh-portal`.

## Smoke test

After install:

```sh
kubectl -n fdh rollout status statefulset/fdh-portal-fdh-portal-api --timeout=120s
kubectl -n fdh rollout status deploy/fdh-portal-fdh-portal-web --timeout=120s

# Tunnel to the API and check the catalog:
kubectl -n fdh port-forward svc/fdh-portal-fdh-portal-api 18080:8080 &
curl -s http://localhost:18080/api/v1/skills | jq '.items | length'
```

If the response is a positive integer, the API has reached the registry
and refreshed its catalog. Open the portal at `https://<host>`.

## What's NOT in this chart

- Keycloak itself (use the existing instance at `keycloak.askenaz.dev`).
- Prometheus / OTel collector — assumed to be running in the cluster
  (the chart only enables the ServiceMonitor + emits OTLP).
- A database, **by default** — the API is stateless apart from its per-pod
  hub-clone PVC (a rebuildable cache of the catalog, not a datastore). An
  OPTIONAL in-cluster Postgres usage-telemetry store can be enabled with
  `telemetry.store.enabled=true` (hub-usage-telemetry, Phase 2); it holds only
  anonymous/pseudonymous TELEMETRY + USER-DATA, never catalog CONFIG, and is
  NON-FATAL to the API at boot (an unreachable store never crashes the portal
  nor blocks anonymous catalog reads). When the flag is off, no Postgres /
  Secret renders and the API gets no `FDH_TELEMETRY_DSN`.
- The GitHub App ("the bot") registration/install — that is an **org-owner**
  action (register the App with Contents:write + Pull-requests:write on the hub
  repo only, install it, provision its id + private key into a Secret). The chart
  only *wires* that Secret into the API via `secretKeyRef`, gated by
  `gitops.enabled` (default off). Until provisioned, the web → Git write surface
  ships **dark**: the API boots, catalog/admin reads serve, and write endpoints
  return a typed 503 `gitops_not_configured` (portal-gitops-write,
  portal-runtime-resilience). The bot is **propose-only** — it can open PRs but
  never merge, so a leaked token cannot corrupt the catalog.
- Code signing or supply-chain attestation — see the `ops-readiness`
  change for that work.
