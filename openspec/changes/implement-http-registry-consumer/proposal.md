## Why

El hub `forge-development-hub` está aterrizando el change `add-http-registry-transport` que define el wire protocol HTTP que el `Registry` interface del CLI puede consumir sin git. La spec original (`installer-core` → `skill-bundle-and-registry > Registry interface abstraction`) ya prometía que "future implementations (e.g. `HTTPRegistry`) MUST be substitutable without changes to command code", pero el código Go en `C:/forge/fdh/pkg/registry/` sólo tiene `GitRegistry` y el dispatcher `internal/cli/context.go:60-87` sólo conoce esa implementación.

Como consecuencia, el primer run de `fdh init` en una máquina limpia hace un `git clone` invisible que demora minutos y deja ~150 MB en `~/Library/Application Support/fdh/registry-cache/...`. Para Macs corporativos con `git` deshabilitado, máquinas air-gapped que usan mirror estático en S3, o el portal `fdh.askenaz.dev` que ya sirve los browse endpoints — no hay forma hoy de consumir esos hostings.

Este change implementa en Go el HTTPRegistry que satisface el wire protocol del spec del hub, y extiende el dispatcher para escogerlo por configuración. El comportamiento user-facing del wire protocol vive en el spec del hub; este change deja sólo el contrato de implementación Go (estructura del package, exit codes, JSON output, env vars).

## What Changes

- **Nuevo `pkg/registry/http.go`** con `HTTPRegistry` struct implementando la interface `Registry` (`Index`, `Manifest`, `FetchBundle`, `Search`, `CheckConsistency`, `Source`). Usa `net/http` stdlib + `crypto/sha256` que ya importa el repo — cero dependencias nuevas.
- **Cache local de archivos HTTP** en `<userConfigDir>/fdh/http-cache/<host>/<path>` keyed por SHA-256, con respeto a `ETag` y `Cache-Control: immutable` para evitar re-descargas innecesarias. Bundles inmutables se guardan permanentemente; `index.json` y `manifest.json` revalidan con `If-None-Match` al expirar el `max-age`.
- **Verificación SHA-256 obligatoria antes de escribir**: `FetchBundle` baja `bundle.tar.gz` + `bundle.sha256`, computa el hash localmente, aborta sin extraer si no coincide. Cumple `skill-bundle-and-registry > Hash verification before write` por el camino HTTP.
- **Dispatcher en `internal/cli/context.go > buildRegistry`** lee la config key nueva `registry.kind` (`auto` | `git` | `http`). En `auto` (default), aplica la heurística por URL: `.git` o `git@/ssh://` → GitRegistry; `https://` o `http://` sin sufijo `.git` → HTTPRegistry; `file://` → local-path. `registry.kind` explícito fuerza el dispatcher independiente de la URL.
- **Nuevas config keys** en `internal/cli/config.go > SupportedConfigKeys`: `registry.kind`, `registry.http.api_version` (default `v1`), `registry.http.auth.bearer`, `registry.http.auth.basic.user`, `registry.http.auth.basic.pass`, `registry.http.auth.client_cert`, `registry.http.auth.client_key`.
- **Env vars equivalentes** para CI/dev workflows: `FDH_REGISTRY_KIND`, `FDH_REGISTRY_HTTP_BEARER`, `FDH_REGISTRY_HTTP_API_VERSION`. Honoran el override de viper como las existentes.
- **JSON output additive**: `fdh doctor --json` extiende su payload con `registry.kind` y `registry.transport` (string descriptivo: `"git:https://...,clone at /path"` o `"http:https://...,api=v1"`). Sin breaking — sólo campos opcionales nuevos.
- **`fdh doctor` reporta el transporte activo** en human output: una línea adicional `registry transport: http v1` (o `git`, o `local`).
- **Tests E2E** contra un `httptest.Server` que sirve el árbol de `internal/testutil/fixtures/registry/` bajo `/v1/`. Verifica `install`, `search`, `update`, `doctor` paridad contra el equivalente GitRegistry.
- **GitRegistry sin cambios**. La implementación existente queda intacta; sigue siendo el default cuando la URL termina en `.git` o cuando el usuario explícita `registry.kind=git`.

## Capabilities

### New Capabilities

<!-- Este change no agrega capabilities user-facing en este repo. La capability `hub-http-registry` vive en el hub (es la fuente de verdad de UX). -->

### Modified Capabilities

- `fdh-cli-implementation-contract`: agrega requirements sobre el package layout del HTTPRegistry (`pkg/registry/http.go`), nuevas config keys y env vars, formato del cache HTTP en disco, exit codes que el HTTPRegistry comparte con GitRegistry, y el dispatcher heuristic en `buildRegistry`. NO duplica el wire protocol — referencia `hub-http-registry` del hub.

## Impact

- **Contrato externo:** el spec `hub-http-registry` del hub (`forge-development-hub/openspec/specs/hub-http-registry/spec.md`) es la fuente de verdad del wire protocol HTTP. Este change implementa el cliente Go que lo consume. Debe estar archivado (synced a `openspec/specs/`) antes del apply.
- **Nuevo archivo Go:** `pkg/registry/http.go` (~400-500 líneas estimadas: struct + interface impl + cache + retry/backoff + auth headers).
- **Nuevo archivo de tests:** `pkg/registry/http_test.go` con httptest server sirviendo fixture registry.
- **Modificación menor:** `internal/cli/context.go > buildRegistry` agrega 20-30 líneas para el dispatcher con heurística URL.
- **Modificación menor:** `internal/cli/config.go > SupportedConfigKeys` agrega ~7 entries nuevas.
- **Modificación menor:** `internal/cli/env.go` (si existe el mapping env → viper) agrega las nuevas env vars.
- **Modificación menor:** `internal/cli/doctor.go` agrega el report del transporte activo.
- **Cero nuevas dependencias Go:** todo con stdlib (`net/http`, `crypto/sha256`, `encoding/json`, `archive/tar`, `compress/gzip` — ya importadas).
- **Sin breaking changes:** comandos existentes y JSON output mantienen forma. La heurística `auto` preserva el comportamiento actual cuando la URL termina en `.git`.
- **Migración del cache:** HTTPRegistry usa `<userConfigDir>/fdh/http-cache/` separado del `<userConfigDir>/fdh/registry-cache/` que usa GitRegistry. Cero conflicto entre ambos transports en la misma máquina.
- **CI:** el job `task test` debe ejecutar los tests nuevos. `task e2e` agrega un caso E2E que corre los comandos contra un fixture HTTP server.
- **Performance:** primer `fdh init` con HTTPRegistry baja <5 MB total vs ~150 MB con GitRegistry. Hot path para `fdh install <ns>/<name>` son 4 GETs (index.json + manifest.json + bundle.tar.gz + bundle.sha256).
- **Coexistencia:** el mismo binario `fdh` soporta ambos transports. Proyectos pueden migrar uno a uno sin coordinación global.
