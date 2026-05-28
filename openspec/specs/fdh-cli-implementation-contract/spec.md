# fdh-cli-implementation-contract Specification

## Purpose
TBD - created by archiving change implement-http-registry-consumer. Update Purpose after archive.
## Requirements
### Requirement: HTTPRegistry implementation lives in pkg/registry/http.go

El binario `fdh` SHALL incluir una implementación Go `HTTPRegistry` de la interface `Registry` ubicada en `pkg/registry/http.go`, en el mismo Go package que `pkg/registry/git.go` y `pkg/registry/registry.go`. La implementación SHALL satisfacer la interface vía un static check `var _ Registry = (*HTTPRegistry)(nil)`.

#### Scenario: Static check verifica la interface

- **WHEN** el código se compila con `go build ./...`
- **THEN** la compilación tiene éxito y `*HTTPRegistry` es asignable a `Registry`

#### Scenario: HTTPRegistry implementa todos los métodos

- **WHEN** se invoca `Index(ctx)`, `Manifest(ctx, ns, name)`, `FetchBundle(ctx, ns, name, ver)`, `Search(ctx, query)`, `CheckConsistency(ctx)`, o `Source()` sobre un `*HTTPRegistry`
- **THEN** cada método devuelve resultados conformes al contrato del interface (mismas signatures, mismos error types que GitRegistry usa)

### Requirement: Dispatcher selecciona transporte por config y heurística URL

El dispatcher `buildRegistry` en `internal/cli/context.go` SHALL leer la config key `registry.kind` con valores válidos `auto` (default), `git`, o `http`. En modo `auto`, el dispatcher SHALL aplicar la heurística por URL:

- URL termina en `.git` o empieza con `git@`, `ssh://`, `git://` → instancia `GitRegistry`.
- URL empieza con `https://` o `http://` (y no termina en `.git`) → instancia `HTTPRegistry`.
- `registry.local_path` está seteado → ignora `registry.url` y usa GitRegistry en modo local-path (comportamiento existente).
- Caso contrario → error con mensaje que nombra la URL.

`registry.kind=git` o `registry.kind=http` explícito SHALL forzar el dispatcher independiente de la URL.

#### Scenario: URL .git resuelve a GitRegistry en modo auto

- **WHEN** `registry.url=https://github.com/askenaz-dev/forge-development-hub.git` y `registry.kind` no seteado (o `auto`)
- **THEN** `buildRegistry` devuelve un `*GitRegistry`; `Source()` devuelve string con prefijo `git:`

#### Scenario: URL https sin .git resuelve a HTTPRegistry en modo auto

- **WHEN** `registry.url=https://pkg.askenaz.dev/registry/v1/` y `registry.kind` no seteado
- **THEN** `buildRegistry` devuelve un `*HTTPRegistry`; `Source()` devuelve string con prefijo `http:`

#### Scenario: registry.kind=git fuerza GitRegistry contra URL https

- **WHEN** `registry.url=https://pkg.askenaz.dev/registry/v1/` y `registry.kind=git`
- **THEN** el dispatcher intenta instanciar `GitRegistry` con esa URL; la URL es válida-como-clone si el server expone Git smart protocol, o falla con error de clone en runtime

#### Scenario: registry.kind=http fuerza HTTPRegistry contra URL .git

- **WHEN** `registry.url=https://github.com/askenaz-dev/forge-development-hub.git` y `registry.kind=http`
- **THEN** el dispatcher instancia `HTTPRegistry` con esa URL; el primer GET a `<url>/index.json` probablemente devuelve HTML del repo de GitHub y el cliente falla con un error de parsing, pero la elección de transport respeta el override

#### Scenario: Auto sin URL clara devuelve error accionable

- **WHEN** `registry.url=foo://bar.example.com` y `registry.kind=auto`
- **THEN** `buildRegistry` retorna un error con mensaje que nombra la URL y sugiere setear `registry.kind` explícito

### Requirement: Nuevas config keys soportadas

El comando `fdh config set <key> <value>` SHALL aceptar las siguientes keys nuevas, agregadas al map `SupportedConfigKeys` en `internal/cli/config.go`:

| Key                                  | Valores                                            | Default |
| ------------------------------------ | -------------------------------------------------- | ------- |
| `registry.kind`                      | `auto`, `git`, `http`                              | `auto`  |
| `registry.http.api_version`          | `v1` (futuro: `v2`)                                | `v1`    |
| `registry.http.auth.bearer`          | string (token opaque)                              | ``      |
| `registry.http.auth.basic.user`      | string                                             | ``      |
| `registry.http.auth.basic.pass`      | string                                             | ``      |
| `registry.http.auth.client_cert`     | absolute path a archivo PEM                        | ``      |
| `registry.http.auth.client_key`      | absolute path a archivo PEM                        | ``      |

Las keys existentes (`registry.url`, `registry.local_path`, `registry.branch`, `defaults.scope`, `cache.dir`, `adapters.override`) SHALL mantener su comportamiento sin cambios.

#### Scenario: Config set acepta registry.kind=http

- **WHEN** se ejecuta `fdh config set registry.kind http`
- **THEN** el comando termina con exit 0 y persiste `registry.kind: http` en `<userConfigDir>/fdh/config.yaml`

#### Scenario: Config get retrieves bearer token

- **WHEN** previamente se ejecutó `fdh config set registry.http.auth.bearer abc123` y luego `fdh config get registry.http.auth.bearer`
- **THEN** stdout contiene exactamente `abc123`

#### Scenario: Config list incluye nuevas keys

- **WHEN** se ejecuta `fdh config list`
- **THEN** la salida tabular incluye las 7 keys nuevas con sus valores actuales y descripciones

#### Scenario: Key desconocida devuelve exit 2

- **WHEN** se ejecuta `fdh config set registry.http.unknown_key foo`
- **THEN** el comando termina con exit code 2 y stderr menciona la key inválida con la lista de keys válidas

### Requirement: Env vars override viper para nuevas keys

El binario `fdh` SHALL aceptar override por env var de las nuevas config keys, con el mapping:

| Env var                                 | Viper key                          |
| --------------------------------------- | ---------------------------------- |
| `FDH_REGISTRY_KIND`                     | `registry.kind`                    |
| `FDH_REGISTRY_HTTP_API_VERSION`         | `registry.http.api_version`        |
| `FDH_REGISTRY_HTTP_BEARER`              | `registry.http.auth.bearer`        |
| `FDH_REGISTRY_HTTP_BASIC_USER`          | `registry.http.auth.basic.user`    |
| `FDH_REGISTRY_HTTP_BASIC_PASS`          | `registry.http.auth.basic.pass`    |
| `FDH_REGISTRY_HTTP_CLIENT_CERT`         | `registry.http.auth.client_cert`   |
| `FDH_REGISTRY_HTTP_CLIENT_KEY`          | `registry.http.auth.client_key`    |

Env vars SHALL tener precedencia sobre `config.yaml` y SHALL ser leídas en cada invocación del CLI (no requieren `fdh config set`).

#### Scenario: Env override fuerza transport http

- **WHEN** `config.yaml` contiene `registry.kind: git` y se invoca `FDH_REGISTRY_KIND=http fdh search owasp`
- **THEN** la invocación usa HTTPRegistry; al volver a invocar sin la env var, vuelve a usar GitRegistry

#### Scenario: Bearer via env var

- **WHEN** se invoca `FDH_REGISTRY_HTTP_BEARER=abc123 fdh search owasp` contra un registry HTTP
- **THEN** las requests llevan `Authorization: Bearer abc123` sin necesidad de haber persistido el token en `config.yaml`

### Requirement: Cache HTTP en disco con verificación SHA-256

`HTTPRegistry` SHALL usar un cache local en disco bajo `<userConfigDir>/fdh/http-cache/` (o `<userCacheDir>/fdh/` si el OS distingue config de cache). El cache SHALL ser content-addressed: cada blob descargado se guarda en `objects/<sha256-prefix>/<sha256-full>.bin`. El cache SHALL mantener archivos `.meta` JSON keyed por URL para lookup eficiente y para almacenar ETag y headers de cache. El cache SHALL ser independiente del cache de GitRegistry y SHALL coexistir sin conflicto.

#### Scenario: Bundle inmutable cacheado y reusado

- **WHEN** un cliente baja `https://pkg.askenaz.dev/registry/v1/skills/security/owasp-quick-review/versions/1.0.0/bundle.tar.gz` por primera vez
- **THEN** el archivo queda en `<cache>/objects/<sha>/<sha-completo>.bin` y un `.meta` registra la URL y el ETag

#### Scenario: Re-fetch reusa cache sin GET

- **WHEN** una segunda invocación del CLI necesita el mismo bundle (mismo `bundle.tar.gz`)
- **THEN** el HTTPRegistry detecta el cache hit por path local, NO emite GET HTTP, y devuelve el bundle inmediatamente

#### Scenario: Revalidación con If-None-Match para index.json

- **WHEN** el cache de `index.json` excedió su `max-age` y el cliente lo necesita
- **THEN** el cliente emite `GET .../index.json` con header `If-None-Match: "<etag-anterior>"`; si el server responde 304, el cliente actualiza el `.meta` con un nuevo timestamp pero NO re-baja el JSON

### Requirement: SHA-256 verification antes de extraer el bundle

`HTTPRegistry.FetchBundle(ctx, ns, name, ver)` SHALL bajar el `bundle.sha256` sidecar antes del `bundle.tar.gz`, parsear el hash esperado, y comparar contra el SHA-256 computado del tarball descargado. Si los hashes no coinciden, `FetchBundle` SHALL retornar un `registry.HashMismatch` (que el CLI mapea a `ExitGenericFailure=1` por compatibilidad con GitRegistry) y NO SHALL escribir ningún archivo extraído al filesystem target.

#### Scenario: Hash match — extrae y devuelve BundlePath

- **WHEN** el SHA-256 computado del `bundle.tar.gz` coincide con `bundle.sha256` Y con `manifest.json.versions[ver].content_hash`
- **THEN** `FetchBundle` extrae a un temp dir, mueve atomicamente a `<cache>/extracted/<sha>/`, y devuelve un `BundlePath` con el path absoluto + hash

#### Scenario: Hash mismatch — aborta sin escribir

- **WHEN** el SHA-256 computado difiere del `bundle.sha256`
- **THEN** `FetchBundle` retorna `registry.HashMismatch{Expected, Got}`; ningún path del filesystem target es escrito; el error message incluye ambos hashes

#### Scenario: Sidecar faltante — aborta antes de bajar el tarball

- **WHEN** `GET <base>/v1/skills/<ns>/<name>/versions/<v>/bundle.sha256` responde 404
- **THEN** `FetchBundle` retorna un error envuelto sin emitir el GET del `bundle.tar.gz`

### Requirement: Doctor reporta el transporte activo

`fdh doctor` SHALL reportar en su salida human (default) una línea identificando el transporte del registry activo. La salida JSON (`fdh doctor --json`) SHALL incluir los campos opcionales `registry.kind` (string: `git` | `http` | `local`) y `registry.transport` (string descriptivo human-friendly). Los campos JSON existentes (`registry.source`, `registry.reachable`, etc.) SHALL mantener su forma.

#### Scenario: Doctor human muestra transport line

- **WHEN** se ejecuta `fdh doctor` con `registry.kind=http`
- **THEN** stdout incluye una línea como `registry transport: http v1` o similar, identificando claramente el transporte

#### Scenario: Doctor JSON incluye kind y transport

- **WHEN** se ejecuta `fdh doctor --json` con `registry.kind=http`
- **THEN** el JSON output incluye `"registry": {"kind": "http", "transport": "http v1", "source": "...", "reachable": ..., ...}`

#### Scenario: Doctor JSON mantiene campos previos

- **WHEN** se ejecuta `fdh doctor --json` con configuración existente (git registry)
- **THEN** los campos pre-existentes (`source`, `reachable`, etc.) tienen los mismos tipos y semántica; los campos nuevos (`kind`, `transport`) son additive

### Requirement: HTTPRegistry exit codes alineados con GitRegistry

Errores en `HTTPRegistry` SHALL mappearse a los exit codes existentes documentados en `docs/exit-codes.md` y declarados en `internal/cli/errors.go`: `3 = ExitRegistryUnreach` para network errors / 5xx tras retries, `6 = ExitPermission` para cache dir no escribible u otros filesystem-permission failures, `1 = ExitGenericFailure` para SHA-256 mismatch (compatible con el comportamiento existente del `GitRegistry`, que retorna `HashMismatch` mapeado a 1). NO SHALL introducir exit codes nuevos exclusivos del transport HTTP. Esta lista reemplaza el draft anterior (`3/4/5`) que estaba en conflicto con los pilots existentes — ver la nota "Why exit 3 is not permission denied" en `docs/exit-codes.md`.

#### Scenario: Network failure mapea a exit 3

- **WHEN** el server HTTP es inalcanzable (timeout, connection refused, DNS failure) y los 3 retries fallan
- **THEN** el CLI termina con exit code 3 (`ExitRegistryUnreach`) y stderr menciona la URL y el último error de red

#### Scenario: SHA-256 mismatch mapea a exit 1

- **WHEN** el hash del bundle no matchea el sidecar
- **THEN** `FetchBundle` retorna `registry.HashMismatch{...}` que el CLI mapea a exit code 1 (`ExitGenericFailure`), mismo comportamiento que GitRegistry

#### Scenario: Cache dir no escribible mapea a exit 6

- **WHEN** `<userConfigDir>/fdh/http-cache/` no es escribible (permisos)
- **THEN** el CLI termina con exit code 6 (`ExitPermission`) y stderr menciona el path no escribible

### Requirement: Cero nuevas dependencias Go

La implementación de `HTTPRegistry` SHALL usar solamente packages de la stdlib Go: `net/http`, `crypto/sha256`, `encoding/json`, `archive/tar`, `compress/gzip`, `os`, `io`, `path/filepath`, `strings`, `errors`, `fmt`, `context`, `time`, `sync`, `crypto/tls`. NO SHALL introducir dependencias externas nuevas en `go.mod`.

#### Scenario: go.mod sin entries nuevas

- **WHEN** se compara `go.mod` antes y después de aplicar este change
- **THEN** las únicas modificaciones permitidas son cambios al `require` block que no añadan packages externos nuevos (e.g., upgrades de versión menores son OK; nuevos paths no)

### Requirement: GitRegistry sin cambios de comportamiento

Este change NO SHALL modificar las signatures públicas, comportamiento observable, ni paths de cache del `GitRegistry` existente en `pkg/registry/git.go`. Configuraciones que usaban `registry.url` apuntando a una URL `.git` SHALL seguir produciendo resultados idénticos al comportamiento previo al change.

#### Scenario: Configuración Git existente sigue funcionando

- **WHEN** un usuario con `registry.url=https://github.com/askenaz-dev/forge-development-hub.git` y sin tocar `registry.kind` ejecuta `fdh init` después del upgrade
- **THEN** el CLI usa `GitRegistry` con el mismo behavior y mismo cache dir que antes del upgrade

#### Scenario: Tests existentes de GitRegistry siguen pasando

- **WHEN** se ejecuta `go test ./pkg/registry/...`
- **THEN** todos los tests pre-existentes (`git_test.go`, `git_refresh_test.go`) pasan sin modificación

