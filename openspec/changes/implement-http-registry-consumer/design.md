## Context

El CLI `fdh` (Go + cobra + viper) tiene hoy una sola implementación de `Registry`: `GitRegistry` en `C:/forge/fdh/pkg/registry/git.go`. Ese archivo usa `github.com/go-git/go-git/v5` para clonar el remoto a `~/Library/Application Support/fdh/registry-cache/<sanitized-url>/` la primera vez, y `fetch + checkout` en los runs subsecuentes. El dispatcher `internal/cli/context.go > buildRegistry` (líneas 60-87) decide entre `LocalPath` (carpeta pre-poblada, sin git) y `RemoteURL` (clone), pero siempre instancia `*GitRegistry`.

El interface `Registry` en `pkg/registry/registry.go:16-40` ya está diseñado para multiple implementations:

```go
type Registry interface {
    Index(ctx context.Context) (Index, error)
    Manifest(ctx context.Context, namespace, name string) (Manifest, error)
    FetchBundle(ctx context.Context, namespace, name, version string) (BundlePath, error)
    Search(ctx context.Context, query string) ([]SkillSummary, error)
    CheckConsistency(ctx context.Context) []ConsistencyIssue
    Source() string
}
```

El hub `forge-development-hub` aterrizó el change `add-http-registry-transport` que define el wire protocol HTTP — URLs canónicas bajo `/v1/`, SHA-256 sidecar obligatorio, ETag + Cache-Control, autenticación opcional por bearer/basic/mTLS. Cualquier static file server que sirva el árbol del registry sobre HTTP cumple el contrato; el portal-api también lo cumple bajo `/registry/v1/*`.

Stakeholders del cliente Go: developers usando `fdh init` en máquinas sin git; CI pipelines air-gapped; el equipo de plataforma operando el portal; mantenedores del repo `fdh` que querían cerrar el TODO del HTTPRegistry desde `installer-core`.

Constraints duras:
- Sin nuevas dependencias Go. El CLI ya importa `net/http`, `crypto/sha256`, `encoding/json`, `archive/tar`, `compress/gzip` — todo lo que hace falta.
- `GitRegistry` sin breaking changes. Hay pilots corriendo contra `.git` URLs.
- Idempotencia del cache HTTP — re-runs no deben re-descargar archivos inmutables.
- Tests E2E reproducibles con `httptest.Server` sirviendo `internal/testutil/fixtures/registry/`.

## Goals / Non-Goals

**Goals:**

- `pkg/registry/http.go` implementa la interface `Registry` y pasa `var _ Registry = (*HTTPRegistry)(nil)` static check.
- Cache local en disco con verificación por SHA-256 — los bundles inmutables nunca se re-descargan en runs siguientes.
- Revalidación condicional para `index.json` y `manifest.json` vía `If-None-Match` + ETag → `304 Not Modified` no escribe nada.
- Dispatcher en `buildRegistry` escoge transport por config + heurística URL, sin tocar código de comandos (`install.go`, `search.go`, `doctor.go`, `update.go`, `list.go`, `init.go`).
- Autenticación opcional: bearer token, basic auth, mTLS via cert+key. Configurable per-machine via viper, env vars, o flags.
- `fdh doctor` reporta el transporte activo en human + JSON output.
- E2E tests cubren `install`, `search`, `update`, `doctor` por la ruta HTTP equivalente a la ruta Git.
- Performance objetivo: primer `fdh init` con HTTPRegistry baja <5 MB total para los skills default.

**Non-Goals:**

- **No** implementar OIDC flow (Keycloak interactivo). El wire protocol del hub deja eso fuera de `/v1/`; este change también.
- **No** soportar `file://` URLs como nuevo modo del HTTPRegistry — eso ya está cubierto por `registry.local_path` en `buildRegistry`.
- **No** tocar el código de comandos. Si algún comando requiere ajuste para soportar HTTP, ese ajuste sale a un change separado.
- **No** romper `GitRegistry` ni cambiar su signature pública. Es additive-only sobre el package.
- **No** publicar a un registry HTTP (write). El interface es read-only; publicar bundles sigue siendo via git push al hub.
- **No** implementar resumption de descargas parciales o range requests. Bundles son ~kBs; full GET es suficiente.
- **No** mover el cache existente de GitRegistry. Los dos caches coexisten en directorios separados.

## Decisions

### Decision 1: Package layout — `pkg/registry/http.go` + `pkg/registry/http_test.go`

Un archivo nuevo `http.go` con todo el `HTTPRegistry` (struct + métodos del interface + helpers internos), y un `http_test.go` con tests unitarios contra `httptest.Server`. NO sub-package nuevo (`pkg/registry/http/`) porque el código es chico y compartir el mismo package con `git.go` facilita reusar tipos (`Index`, `Manifest`, `BundlePath`, `ConsistencyIssue`) sin importar desde otro package.

Trade-off considerado: un sub-package por transport. Descartado porque obligaría a importar tipos circularmente o duplicarlos. El layout actual del package (`registry.go` define interface + tipos; `git.go` y ahora `http.go` implementan) es idiomatico Go.

### Decision 2: Struct shape del HTTPRegistry

```go
type HTTPRegistry struct {
    BaseURL    string         // e.g. "https://pkg.askenaz.dev/registry/v1/" — termina con "/"
    APIVersion string         // "v1" (default); reservado para "v2" futuro
    CacheDir   string         // absolute path para el cache local HTTP
    HTTPClient *http.Client   // configurable para retries/timeouts/proxies/TLS custom
    Auth       HTTPAuth       // bearer/basic/mTLS — zero value = sin auth
    Logger     func(line string)
}

type HTTPAuth struct {
    Bearer       string  // si != "" → Authorization: Bearer <bearer>
    BasicUser    string  // si != "" → Authorization: Basic ...
    BasicPass    string
    ClientCert   string  // path al PEM del cert cliente (mTLS)
    ClientKey    string  // path al PEM de la key cliente (mTLS)
}
```

La auth se aplica armando un `http.Client` con TLS config (para mTLS) y un transport wrapper que añade el header `Authorization`. Si todos los campos están vacíos → sin auth.

`BaseURL` siempre termina en `/` para que `BaseURL + "skills/<ns>/..."` arme URLs sanas. El parser de config normaliza.

### Decision 3: Cache HTTP en disco — keyed por SHA-256, no por URL

`CacheDir` default: `<userConfigDir>/fdh/http-cache/`. Layout:

```
http-cache/
├── objects/
│   └── ab/                                  # primeros 2 hex chars del sha
│       └── ab12cd...full-sha.bin            # contenido raw del archivo
├── index/
│   └── pkg.askenaz.dev/
│       └── registry/v1/
│           ├── index.json.meta              # {"sha256":"...","etag":"...","fetched_at":"..."}
│           └── skills/security/owasp-quick-review/
│               ├── manifest.json.meta
│               └── versions/1.0.0/
│                   ├── bundle.tar.gz.meta   # {"sha256":"...","cache_control":"...","content_type":"..."}
│                   └── bundle.sha256.meta
```

`objects/<sha>` guarda el blob real; `index/<host>/<path>.meta` guarda metadatos. Múltiples URLs apuntando al mismo SHA comparten el objeto (e.g. dos `bundle.tar.gz` idénticos en dos namespaces distintos). El meta es chico (JSON) y describe el ETag para futuras revalidaciones.

Trade-off considerado: cache keyed por URL (clásico HTTP cache). Descartado porque los objects content-addressed son más robustos a redirects, mirrors, y deduplicación. El meta keyed por URL hace el lookup eficiente sin perder los beneficios.

### Decision 4: Heurística de selección en el dispatcher

`internal/cli/context.go > buildRegistry`:

```go
func buildRegistry(verbose bool) (registry.Registry, error) {
    local := viper.GetString("registry.local_path")
    if local != "" {
        // local-path mode: no transport
        return &registry.GitRegistry{LocalPath: local, RemoteURL: "", ...}, nil
    }

    remote := viper.GetString("registry.url")
    kind := viper.GetString("registry.kind")  // "auto" | "git" | "http"
    if kind == "" { kind = "auto" }

    switch kind {
    case "git":
        return buildGitRegistry(remote, verbose), nil
    case "http":
        return buildHTTPRegistry(remote, verbose)
    case "auto":
        if isGitURL(remote) { return buildGitRegistry(remote, verbose), nil }
        if isHTTPURL(remote) { return buildHTTPRegistry(remote, verbose) }
        return nil, errors.New("cannot auto-detect registry transport from " + remote)
    default:
        return nil, fmt.Errorf("unknown registry.kind: %q", kind)
    }
}

func isGitURL(u string) bool {
    return strings.HasSuffix(u, ".git") ||
           strings.HasPrefix(u, "git@") ||
           strings.HasPrefix(u, "ssh://") ||
           strings.HasPrefix(u, "git://")
}

func isHTTPURL(u string) bool {
    return strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")
}
```

`isGitURL` se chequea ANTES que `isHTTPURL` para que `https://github.com/foo/bar.git` resuelva a Git, no a HTTP — el `.git` suffix es discriminador.

### Decision 5: Fetch path para `bundle.tar.gz` — sidecar obligatorio, verify-then-extract

`FetchBundle(ctx, ns, name, ver)`:

1. Bajar `<base>/v1/skills/<ns>/<name>/versions/<ver>/bundle.sha256` (chico, ~kB). Parse → `expectedSHA`.
2. Cross-check contra `manifest.json.versions[ver].content_hash` (ya en cache local probablemente). Si no matchea → abortar, `ExitValidationFailed`.
3. Bajar `<base>/v1/skills/<ns>/<name>/versions/<ver>/bundle.tar.gz`. Stream-hash mientras descarga (no necesita full buffer en memoria).
4. Si SHA computado != `expectedSHA` → abortar, no escribir nada, `ExitValidationFailed`.
5. Extraer a temp dir; mover atomicamente a `<cache>/extracted/<sha>/`. Devolver `BundlePath{Path: ..., Hash: expectedSHA, cleanup: ...}`.

Cumple `skill-bundle-and-registry > Hash verification before write` por el path HTTP.

### Decision 6: Revalidación condicional — solo para `index.json` y `manifest.json`

Para `bundle.tar.gz` y `bundle.sha256`, el `Cache-Control: immutable` del wire protocol significa que una vez cacheados, nunca se re-bajan. Cache hit es trivial: si el objeto existe en `<cache>/objects/<sha>/`, no hago GET.

Para `index.json` y `manifest.json`:
- Si `time.Now() < meta.fetched_at + 5min` → cache hit, no GET.
- Si excedió `max-age` → GET con `If-None-Match: <stored-etag>`.
- Server responde 304 → actualizar `meta.fetched_at`, reusar el objeto cacheado.
- Server responde 200 → parsear body, computar SHA, guardar objeto + meta nueva.

Esto mantiene el `Index` y `Manifest` calls baratos en runs continuos del CLI.

### Decision 7: Retry/backoff — exponencial con jitter, 3 intentos máx

Las requests HTTP usan un `http.Client` con un `Transport` wrapper que aplica retry sobre fallos 5xx, conexiones reset, y timeouts. Backoff exponencial: 100ms, 200ms, 400ms con jitter aleatorio (±25%). Máximo 3 intentos. Después abortar con `RegistryUnreachable` (compatible con el error type que ya devuelve GitRegistry).

NO se retrieva sobre 4xx (cliente error). 401/403 → mensaje accionable sobre `registry.http.auth.*`. 404 → "not found" sin retry.

### Decision 8: Source() string — describe el transporte para `doctor`

`HTTPRegistry.Source()` devuelve `"http:<base>?api=<v>"`, e.g., `"http:https://pkg.askenaz.dev/registry/v1/?api=v1"`. `GitRegistry.Source()` mantiene su formato actual `"git:<url> (clone at <path>)"`.

`fdh doctor` muestra:
```
registry:
  transport: http
  source: https://pkg.askenaz.dev/registry/v1/?api=v1
  reachable: yes (last fetch 2s ago, 3 cache hits, 0 misses)
```

### Decision 9: JSON output extension — additive only

`fdh doctor --json` actual emite `{"registry": {"source": "...", "reachable": bool, ...}}`. Agregamos campos opcionales:

```json
{
  "registry": {
    "source": "https://pkg.askenaz.dev/registry/v1/",
    "kind": "http",
    "transport": "http v1",
    "reachable": true,
    "http_cache_stats": {"hits": 3, "misses": 0, "size_bytes": 42312}
  }
}
```

Campos viejos mantienen forma. Cumple `fdh-cli-implementation-contract > Salida JSON es additive-only`.

### Decision 10: Env vars como override de viper config

Mapping (en `internal/cli/env.go`):

| Env var                                 | Viper key                          |
| --------------------------------------- | ---------------------------------- |
| `FDH_REGISTRY_KIND`                     | `registry.kind`                    |
| `FDH_REGISTRY_HTTP_API_VERSION`         | `registry.http.api_version`        |
| `FDH_REGISTRY_HTTP_BEARER`              | `registry.http.auth.bearer`        |
| `FDH_REGISTRY_HTTP_BASIC_USER`          | `registry.http.auth.basic.user`    |
| `FDH_REGISTRY_HTTP_BASIC_PASS`          | `registry.http.auth.basic.pass`    |
| `FDH_REGISTRY_HTTP_CLIENT_CERT`         | `registry.http.auth.client_cert`   |
| `FDH_REGISTRY_HTTP_CLIENT_KEY`          | `registry.http.auth.client_key`    |

Útiles para CI runners que pasan tokens via env, y para scripts que prefieren no escribir config files persistentes.

## Risks / Trade-offs

- **Risk: cache HTTP crece sin bound.** No tenemos política de eviction. **Mitigation:** un comando futuro `fdh cache clean` (no parte de este change). Hoy es low risk porque bundles son chicos (~kBs cada uno × decenas de skills); el cache total esperado es <50 MB.
- **Risk: revalidación falla cuando el server no implementa ETag/If-None-Match.** **Mitigation:** si el server siempre responde 200 (sin 304), el cache funciona igual pero cada `Index` call re-baja el JSON. Performance degradado pero correcto.
- **Risk: el dispatcher de heurística por URL falla en edge cases** (e.g., proxies que reescriben sufijos). **Mitigation:** `registry.kind` explícito siempre gana. Documentar en `config list`.
- **Risk: mTLS config con paths inválidos genera errores poco accionables.** **Mitigation:** validar paths existen al instanciar el `HTTPRegistry`; fallar fast con mensaje `"client cert path does not exist: /path/..."`.
- **Trade-off:** content-addressed cache es más complejo que URL-keyed. **Justificación:** mejor dedup + más robusto a redirects/mirrors. El meta keyed por URL preserva la simpleza del lookup.
- **Trade-off:** sin OIDC. **Justificación:** scope contenido; OIDC es follow-up cuando aparezca un caso real (registry interno corporativo con SSO obligatorio).

## Migration Plan

1. **Spec landing**: el spec del hub `hub-http-registry` debe estar archivado y synced a `openspec/specs/` del hub antes del apply de este change.
2. **Implementación Go**: `pkg/registry/http.go` + tests, sin tocar `git.go`. Ship el binario con `task build`.
3. **Dispatcher**: `buildRegistry` agrega la heurística + `registry.kind`. Cubierto por tests existentes (con GitRegistry, mantienen verde) + nuevos (HTTPRegistry, contra httptest).
4. **Smoke test manual**: en un Mac limpio sin clone previo, `fdh config set registry.url https://pkg.askenaz.dev/registry/v1/ && fdh init --non-interactive --agents claude-code --skills security/owasp-quick-review` → verificar bundle instalado correctamente.
5. **Release**: ship con `v0.2.0` (minor bump por feature add). Documentar la nueva opción en `docs/quickstart.md` y `docs/install.md`.
6. **Default switch**: en `v0.3.0` o más adelante, considerar si el wizard `fdh init` ofrece HTTP como default. Decisión separada con telemetry behind, no parte de este change.

## Open Questions

- ¿`registry.http.api_version` debe surfacarse en `fdh init` wizard, o es config avanzada? Lean hacia "config avanzada, default v1, no preguntar".
- ¿El cache directory respeta `XDG_CACHE_HOME` en Linux? Probablemente sí — usar `os.UserCacheDir()` en lugar de `os.UserConfigDir()` para el path del HTTP cache. Decisión menor del impl.
- ¿Cómo se invalida el cache cuando el usuario hace `fdh config set registry.url <otra-url>`? Lo más seguro es: el cache es per-host (ya por el layout `<cache>/index/<host>/`), entonces cambiar URL es transparente; cada host tiene su sub-tree.
