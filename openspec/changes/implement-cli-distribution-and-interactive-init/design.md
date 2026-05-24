## Context

El repo `fdh` (Go + cobra + viper) ya tiene la base del CLI: `cmd/fdh/`, `internal/cli/` (un archivo por subcomando), `pkg/registry/` (interfaz `Registry` + implementación `GitRegistry`), `pkg/adapters/` (manifest-driven agent path map), `pkg/bundle/` (skill bundle + canonical hash), `pkg/portability/`, `pkg/provenance/`. Hoy `internal/cli/init.go` configura registry+scope+doctor y deja el binario operativo pero **no copia skills** ni es interactivo.

El hub `forge-development-hub` publicó cuatro specs nuevas (`fdh-cli-distribution`, `fdh-init-interactive`, `fdh-skills-sync`, `hub-skills-registry`) que definen el contrato user-facing completo. Este change es la implementación Go de ese contrato, más el pipeline de release/distribución que pone el binario en máquinas de developers con PATH ya seteado.

Stakeholders:
- Developers forge (consumidores): esperan `brew install fdh && fdh init` end-to-end.
- Mantenedores del repo `fdh`: necesitan saber qué exit codes son estables, dónde viven los límites entre packages, cómo extender el catálogo.
- Plataforma forge: opera Artifactory/Nexus/host interno + tap Homebrew interno + winget source interno.
- Hub maintainers: editan `skills/registry.yaml` confiando que `fdh validate-registry` aplica las reglas del spec `hub-skills-registry`.

Constraints duras:
- Compat con `fdh init` actual (flags, env vars, JSON output). Pilots existentes no deben romperse.
- No publicar a registries públicos (Homebrew público, npm, Chocolatey Community, winget public).
- Cert corporativo Authenticode/Apple Developer ID puede no estar disponible al primer release.
- Hub se accede vía git (clone/pull); no hay HTTP API.

## Goals / Non-Goals

**Goals:**

- Implementar los 4 specs del hub en Go con código testeable y modular.
- Pipeline de release que produce binarios per (linux,macos,windows) × (amd64,arm64), los firma cuando hay cert, los publica al host configurado por `FDH_PKG_HOST`, y refresca el manifest.
- `scripts/install.sh` y `scripts/install.ps1` con SHA-256 verification, PATH editing per shell/registry, y soporte para `FDH_PKG_HOST` override.
- Mantener exit codes existentes (`internal/cli/errors.go`) estables; documentar como contrato.
- Wizard TUI que degrada limpio en terminales sin TTY o sin soporte.
- `fdh update` detecta edits locales (hash) y respeta el trabajo del developer (skip + `--force`).

**Non-Goals:**

- **No** modificar las 4 specs del hub. La fuente de verdad de UX es el hub; este change implementa, no redefine.
- **No** firmar binarios en este change (decisión separada cuando plataforma confirme cert).
- **No** publicar a registries públicos.
- **No** servir el catálogo del hub vía HTTP API. Sigue siendo git clone/pull.
- **No** soportar "skills privadas del consumer" en este change (registry es uno solo, autoritativo, en el hub).
- **No** auto-instalar nuevos defaults retroactivamente cuando el admin promueve un skill de `default: false` → `default: true` (los devs lo agregan con `fdh init` o flag dedicada — open question).
- **No** migrar el validator Python del hub a `fdh validate-registry` en este change (es un cambio futuro del hub que reemplaza el script).

## Decisions

### Decision 1: TUI library — `github.com/charmbracelet/huh` para el wizard

Cobra + huh es el stack idiomático actual en Go CLIs interactivos. `huh` provee `Select`, `MultiSelect`, `Confirm`, todos con detección de TTY y fallback automático cuando stdin/stdout no son terminales. Alternativa más antigua (`AlecAivazis/survey/v2`) sigue funcionando pero está menos mantenida.

Trade-off: `huh` agrega ~40 LOC de deps transitivas (bubble tea + lipgloss). Aceptable para el valor en UX.

**Alternativas consideradas:**
- *survey/v2* — descartado: menos mantenido, UX más rústica.
- *Prompts caseros con `bufio.Scanner`* — descartado: re-implementar selección multi-line + checkboxes es scope creep y peor UX.
- *Web UI local (servidor temporal en `localhost`)* — descartado: rompe el principio "CLI puro", añade dependencia de browser.

### Decision 2: YAML — `gopkg.in/yaml.v3` para parsear `registry.yaml` y emitir `.skill-version`

Es el standard de facto en Go. Soporta comentarios al leer y al emitir, y maneja bien tipos heterogéneos (booleanos, listas, mapas anidados). Single dependency well-known.

**Alternativas consideradas:**
- *`sigs.k8s.io/yaml`* — descartado: agrega JSON intermediación; bueno para K8s manifests, overkill aquí.
- *Parser manual* — descartado: ya descartado en el hub (Python+PyYAML); replicar el error en Go es peor.

### Decision 3: Pipeline de release — `goreleaser` con matrix de OS/arch + nfpm

`goreleaser` resuelve en una sola config:
- Cross-compile linux/macos/windows × amd64/arm64.
- Generación de SHA-256 manifests.
- Empaquetado `.deb`/`.rpm` (vía `nfpm` integrado).
- Generación de formula Homebrew (para tap).
- Generación de manifest winget (vía `winget-releaser` o action externa).
- Firma opcional con cosign (no Authenticode/Apple Developer ID, que requieren herramientas dedicadas).

Publicación al host: un step custom en CI que hace `aws s3 cp` / `curl` / `jfrog rt u` según el tipo de host real (decisión de plataforma).

**Alternativas consideradas:**
- *Make + scripts a mano* — descartado: re-implementa lo que `goreleaser` ya hace.
- *Bazel* — descartado: overkill para un CLI Go.

### Decision 4: Adapter de copia — un struct por ecosistema dentro de `pkg/adapters/`

Extender `pkg/adapters/` con un nuevo interface `SkillAdapter`:

```go
type SkillAdapter interface {
    Agent() string                              // "claude-code", "codex", ...
    TargetPath(skillName, projectRoot, scope string) string
    Install(srcDir, targetPath string, opts InstallOpts) (InstallResult, error)
    SupportsSubresources() bool                 // true para Claude/Codex; false para Copilot/OpenCode
}
```

Cuatro implementaciones:
- `ClaudeCodeAdapter`: copia directorio entero, escribe `.skill-version` adentro.
- `CodexAdapter`: idem.
- `CopilotAdapter`: lee SKILL.md, escribe `<name>.prompt.md`, escribe `.skill-version-<name>` al lado.
- `OpenCodeAdapter`: lee SKILL.md, escribe `commands/<name>.md`, escribe `.skill-version-<name>`.

El loop principal en `internal/cli/init.go` itera `for skill in selected: for adapter in selected: adapter.Install(...)`.

**Alternativas consideradas:**
- *Una sola función con switch por agente* — descartado: testear adapters aislados es más limpio.
- *Configurar el mapping en un YAML externo* — descartado: los 4 ecosistemas son fijos en el dominio; agregar nuevo significa código Go nuevo igual.

### Decision 5: Marcador `.skill-version` — YAML, formato fijo, versionado

Formato (ya definido en el spec del hub):
```yaml
name: design-system
hub_version: "2026.05"
hub_commit: abcd1234...
installed_at: 2026-05-23T10:15:00Z
installed_by_fdh: 0.5.2
```

Más un campo extra `content_hash` (sha256 del contenido instalado) que `fdh update` usa para detectar edits locales. Si el `content_hash` registrado no coincide con el hash recomputado al momento del update, hay drift local → skip + warning.

**Alternativas consideradas:**
- *Almacenar hash externo (DB, índice)* — descartado: el marcador per-skill es self-contained y survives reorganization.
- *Sin `content_hash`* — descartado: pierde la detección de edits, que es un requirement del spec.

### Decision 6: `fdh update` — diff resumido + confirmación + filtros

El spec dice "resumen del cambio (lista de archivos modificados/añadidos/borrados, no diff completo)". Implementación:
- Por cada skill candidata, computar set de archivos en HEAD del hub vs set de archivos instalados.
- Imprimir contadores (`+3 added, -1 removed, ~2 modified`) más una lista de paths.
- Para diff completo, el dev puede correr `git diff` manualmente en el hub clonado en cache.

Cache del hub: `$XDG_CACHE_HOME/fdh/hub/` (o `%LOCALAPPDATA%\fdh\hub\` en Windows). Shallow clone + sparse-checkout de `skills/registry.yaml` + `skills/<selected>/` para evitar clonar el hub entero.

**Alternativas consideradas:**
- *Diff completo unified per archivo* — descartado: ruidoso, el dev abre el archivo si quiere ver línea por línea.
- *Sin cache local del hub* — descartado: cada `update` haría un clone completo desde cero, lento.

### Decision 7: Exit codes — estables, documentados en spec

Los exit codes que `errors.go` ya usa se promueven a contrato testeable:
- `0` éxito
- `1` error genérico
- `2` invalid usage (flag mal usada)
- `3` permission denied (no escribe filesystem)
- `4` registry unreachable
- `5` validation failed (registry.yaml malformado, hub corrupto)
- `127` binary not found (usado por el stub legacy)

Cualquier nuevo exit code agregado en este change SHALL preservar los existentes (no renumerar). Nuevos códigos van a partir de `6`.

### Decision 8: JSON output — additive-only, schema versionado implícitamente

`InitResult` y nuevos `UpdateResult`, `ValidateRegistryResult` SHALL:
- Solo agregar campos opcionales (omitirlos si no aplican).
- Nunca renombrar ni cambiar el tipo de un campo existente.
- Nunca cambiar la semántica de un campo (si cambia la semántica, agregar campo nuevo y deprecar el viejo en docs).

Si en el futuro hace falta una v2 incompatible, agregar flag `--json-schema=v2`.

### Decision 9: `FDH_PKG_HOST` env var — leído por scripts, no por `fdh` mismo

`FDH_PKG_HOST` es contrato de los **scripts de install** (`install.sh`, `install.ps1`), no del binario `fdh` después de instalado. El binario `fdh` mismo no lo lee — su única dependencia de red es el git remote del hub (vía `registry.url`).

Default placeholder dentro de los scripts: `pkg.forge.internal`. Override: `FDH_PKG_HOST=mi.host.real ./install.sh`.

**Alternativas consideradas:**
- *Hardcodear host en los scripts* — descartado: requiere rebuild de scripts cuando plataforma confirme el host real.
- *Leer host desde un archivo de config* — descartado: el script de install corre antes de que exista cualquier config; env var es la inyección natural.

### Decision 10: Auto-promoción de defaults — opt-in via `fdh update --include-new-defaults`

Open question del hub: cuando admin promueve un skill a `default: true`, ¿devs ya inicializados lo reciben en el próximo `update`? Decisión:
- Por default NO (`fdh update` sólo refresca lo ya instalado).
- Flag opt-in `fdh update --include-new-defaults` propone instalar los defaults nuevos.
- En docs del hub admin: "promover un default es un cambio de catálogo; comunicar en release notes".

## Risks / Trade-offs

- **TUI library incompatibilidad** (`huh` cambia API en próxima major) → mitigado: pin de versión en go.mod, smoke tests cubriendo wizard en CI.
- **Goreleaser config se vuelve compleja** (4 OS × 2 arch × signing × manifest publishing) → mitigado: dividir en `.goreleaser.yaml` modular + tests del pipeline en CI.
- **Cache del hub corrompido** (kill -9 durante pull) → mitigado: detectar via `git fsck` y re-clone automático con warning.
- **Edits locales falsos positivos** (línea final con/sin `\n`) → mitigado: normalizar EOL antes de hashear o usar `.gitattributes` para forzar LF.
- **Wizard rompe sin alt-screen** (tmux antiguo, Windows Terminal viejo) → mitigado: detectar capability y fallback a prompts línea-a-línea.
- **Manifest publisher falla mid-release** (subió 3 binarios de 4) → mitigado: publicar manifest.json al final, atomic — si falla antes, los binarios sueltos quedan disponibles pero no listados, y `install.sh` muestra "version not found".
- **`registry.yaml` muta entre que init lo lee y update lo refresca** → aceptado: `fdh init` clona en cache y trabaja contra esa snapshot; `update` re-pull explícitamente.

## Migration Plan

No hay migración de datos.

Rollout en fases:
1. **Apply de este change (corto):** crea los specs y tasks en `fdh/openspec/`. CI puede agregar un step `openspec validate --strict`. No toca código.
2. **Implementación Go (largo):** se hace iterando sobre tasks.md. Cada subcomando se puede entregar incrementalmente (init wizard primero, update después, validate-registry último).
3. **Pipeline de release (paralelo a 2):** `goreleaser` config + workflow `release.yml` que se dispara con tag. Pilot con beta tags (`v0.5.0-beta.1`).
4. **Distribución (depende de plataforma):** plataforma confirma `FDH_PKG_HOST`, abre tap Homebrew interno, source winget interno. Scripts publicados al host.
5. **GA:** docs/quickstart.md actualiza con el host real, depreca el flujo manual de tarball.

Rollback: `goreleaser` mantiene versiones anteriores; downgrade manual con `install.sh --version <vN-1>`.

## Open Questions

- **¿Pin `huh` o `survey/v2`?** Decisión en apply al ver la API real contra los wizards descritos en el spec.
- **¿`goreleaser` o herramienta custom para el pipeline?** Decisión en apply al ver constraints de signing y el host real.
- **¿`go install ...` debería seguir funcionando para devs internos con Go toolchain?** Probablemente sí, como atajo no oficial; documentar en docs/install.md.
- **¿Caché del hub se invalida por tiempo o sólo on-demand?** Hoy on-demand (cada `init`/`update` hace `git fetch`). ¿Vale la pena un `--offline` flag que use lo último cacheado sin fetch? Open.
- **¿`fdh validate-registry` se puede usar como pre-commit hook en el hub?** Sí, trivialmente. Documentar al cierre.
