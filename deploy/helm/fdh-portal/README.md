# fdh-portal Helm chart

Deploys the FDH portal (API + Web) onto Kubernetes, fronted by a single
Ingress at the configured host. Keycloak is consumed, not provisioned —
the platform identity team operates the canonical Keycloak.

## Quick start

```sh
# 1. Create the OIDC secret (client_secret + a 32+ byte AUTH_SECRET for Auth.js).
kubectl create secret generic fdh-portal-oidc \
  --from-literal=client_secret=$(openssl rand -hex 32) \
  --from-literal=auth_secret=$(openssl rand -hex 32) \
  --namespace fdh

# 2. Create the role-map ConfigMap.
kubectl create configmap fdh-portal-role-map \
  --from-file=role-map.yaml=./role-map.yaml \
  --namespace fdh

# 3. Install the chart.
helm upgrade --install fdh-portal ./deploy/helm/fdh-portal \
  --namespace fdh --create-namespace \
  --set ingress.host=fdh.forge.internal \
  --set portal.registryURL=https://git.forge.internal/skills/registry.git
```

## Values

See `values.yaml` for the full surface. Key knobs:

| Path                          | Purpose                                                  |
| ----------------------------- | -------------------------------------------------------- |
| `api.image.tag`               | Override the API image tag (default: chart appVersion).  |
| `web.image.tag`               | Override the web image tag.                              |
| `portal.registryURL`          | Git URL of the production skill registry.                |
| `oidc.discoveryURL`           | Keycloak realm discovery URL.                            |
| `oidc.clientSecretRef`        | Reference to the K8s secret holding `client_secret` and `auth_secret`. |
| `ingress.host`                | Public hostname.                                         |
| `observability.otel.endpoint` | OTLP collector for trace export.                         |

## Smoke test

After install:

```sh
kubectl -n fdh wait --for=condition=available deploy/fdh-portal-api --timeout=120s
kubectl -n fdh wait --for=condition=available deploy/fdh-portal-web --timeout=120s

# Tunnel to the API and check the catalog:
kubectl -n fdh port-forward svc/fdh-portal-api 18080:8080 &
curl -s http://localhost:18080/api/v1/skills | jq '.items | length'
```

If the response is a positive integer, the API has reached the registry
and refreshed its catalog. Open the portal at `https://<host>`.

## What's NOT in this chart

- Keycloak itself.
- Prometheus / OTel collector — assumed to be running in the cluster
  (the chart only enables the ServiceMonitor + emits OTLP).
- A database — the MVP is stateless.
- Code signing or supply-chain attestation — see the `ops-readiness`
  change for that work.
