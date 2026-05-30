# Portal API

`fdh-portal-api` serves two URL trees:

| Tree         | Audience                | Auth          | Description                                                                                  |
| ------------ | ----------------------- | ------------- | -------------------------------------------------------------------------------------------- |
| `/api/v1/*`  | UI, scripts, admins     | Optional OIDC | UI catalog, identity, refresh, admin views. Paginated, filterable JSON.                       |
| `/v1/*`      | `pkg/registry` consumer | Anonymous     | HTTP wire protocol — what `fdh init`/`install`/`update` consume from `registry.url`.          |

Both trees serve from the same hub catalog source. The split exists so the
UI can keep its evolving JSON shapes (pagination, filters, `scan_status`)
without breaking the byte-stable wire protocol the CLI depends on.

## Wire protocol (`/v1/*`)

The contract — URL shapes, response bodies, headers, error codes,
auth — is defined by the [`hub-http-registry`
specification](https://github.com/askenaz-dev/forge-development-hub/blob/main/openspec/specs/hub-http-registry/spec.md)
in `forge-development-hub`. The portal-api is one of several
conforming producers (a static-file CDN snapshot is another).

### Endpoints

| Method | Path                                                                   | Cache-Control                                  |
| ------ | ---------------------------------------------------------------------- | ---------------------------------------------- |
| GET    | `/v1/index.json`                                                       | `public, max-age=60, must-revalidate`          |
| GET    | `/v1/{kinds}/{namespace}/{name}/manifest.json`                         | `public, max-age=60, must-revalidate`          |
| GET    | `/v1/{kinds}/{namespace}/{name}/versions/{version}/bundle.tar.gz`      | `public, max-age=31536000, immutable`          |
| GET    | `/v1/{kinds}/{namespace}/{name}/versions/{version}/bundle.sha256`      | `public, max-age=31536000, immutable`          |

Where `{kinds} ∈ {skills, rules, agents, hooks}` and `{namespace}` is
always the literal `forge`. All endpoints emit a strong `ETag` and
honor `If-None-Match` for conditional revalidation (304 Not Modified).

### Determinism guarantees

- `bundle.tar.gz` bytes are reproducible across portal pod respins and
  Go versions. Two requests against the same component+version return
  byte-identical bodies and the same `ETag`. This is what enables
  CDN-level cache hit rates.
- `bundle.sha256` contains the **canonical content hash** (computed
  over the extracted bundle directory, per `pkg/bundle.HashDir`), NOT
  the SHA of the tarball bytes. The CLI extracts the tarball and
  re-hashes the extracted tree, then compares against this sidecar.
  The two hashes describe different things — see
  [`pkg/bundle/tarball.go`](../pkg/bundle/tarball.go) for the
  full reasoning.

### Namespace

For v1 every wire URL uses the literal `forge` namespace. Requests with
any other namespace return 404. The hub's `registry.yaml v2` schema has
no `namespace` field; when multi-tenant namespacing arrives, the wire
protocol gains a real second segment without breaking these URLs.

## Configuration

| Env var                              | Default     | Purpose                                                                            |
| ------------------------------------ | ----------- | ---------------------------------------------------------------------------------- |
| `FDH_PORTAL_API_ADDR`                | `:8080`     | Listen address.                                                                    |
| `FDH_PORTAL_HUB_PATH`                | `/srv/hub`  | **Catalog source for BOTH trees.** Filesystem path where the hub repo is mounted.  |
| `FDH_PORTAL_REGISTRY_LOCAL_PATH`     | —           | Legacy/optional. No longer used by the portal's serving path; kept for the CLI consumer + diagnostics. |
| `FDH_PORTAL_REGISTRY_URL`            | —           | Legacy/optional. No longer used by the portal's serving path.                      |
| `FDH_PORTAL_REGISTRY_BRANCH`         | `main`      | Legacy/optional. No longer used by the portal's serving path.                      |
| `FDH_PORTAL_REFRESH_INTERVAL`        | `60s`       | UI snapshot refresh interval. Wire endpoints read the filesystem on every request. |
| `OIDC_DISCOVERY_URL`                 | —           | OIDC discovery endpoint. Empty disables auth (dev only).                           |
| `OIDC_CLIENT_ID`                     | —           | Expected audience claim.                                                           |
| `OIDC_ROLE_MAP_PATH`                 | —           | YAML role map mounted into the pod.                                                |
| `OTEL_EXPORTER_OTLP_ENDPOINT`        | —           | OTel collector endpoint.                                                           |

The wire endpoints read directly from `FDH_PORTAL_HUB_PATH` on every
request — no snapshot, no refresh loop. The UI endpoints serve an
in-memory snapshot built from the **same** `FDH_PORTAL_HUB_PATH` catalog,
refreshed every `FDH_PORTAL_REFRESH_INTERVAL`. (Earlier builds backed the
UI with a separate `FDH_PORTAL_REGISTRY_*` GitRegistry clone; both trees
now share the one hub source.)

## Deploy with git-sync

In production the hub repo lives at `FDH_PORTAL_HUB_PATH` via a
git-sync sidecar that keeps it fresh. Reference manifest sketch in
[`deploy/portal-api/`](../deploy/portal-api/) (not part of this OpenSpec
change). If the path doesn't exist or doesn't contain
`hub/registry.yaml`, wire endpoints respond `503 Service Unavailable`
with `Retry-After: 5`; the UI endpoints and `/healthz` continue to
work.

## Local development

Point `FDH_PORTAL_HUB_PATH` at your local clone of
`forge-development-hub`:

```sh
FDH_PORTAL_HUB_PATH=/path/to/forge-development-hub \
  go run ./cmd/fdh-portal-api/
curl http://localhost:8080/v1/index.json                 # v2 components[], all four kinds
curl http://localhost:8080/api/v1/skills                 # human catalog (skills view)
curl http://localhost:8080/v1/skills/forge/design-system/manifest.json
# Use a real version from the manifest (e.g. 0.1.0), not the literal "latest":
curl -o bundle.tar.gz http://localhost:8080/v1/skills/forge/design-system/versions/0.1.0/bundle.tar.gz
curl http://localhost:8080/v1/skills/forge/design-system/versions/0.1.0/bundle.sha256
```

Two requests against the same `bundle.tar.gz` URL produce byte-identical
bodies — verify with `diff <(curl ... | sha256sum) <(curl ... | sha256sum)`.
