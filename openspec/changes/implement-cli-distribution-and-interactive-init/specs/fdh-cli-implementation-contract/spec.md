## ADDED Requirements

### Requirement: Exit codes son estables y documentados

El binario `fdh` SHALL preservar los exit codes existentes (`0` éxito, `1` error genérico, `2` invalid usage, `3` permission denied, `4` registry unreachable, `5` validation failed, `127` binary not found) y SHALL documentarlos en `docs/exit-codes.md`. Cualquier exit code nuevo SHALL usar valores ≥ 6 sin renumerar los existentes.

#### Scenario: Exit code conocido en error de permisos

- **WHEN** `fdh init` no puede escribir en `<userConfigDir>/fdh/config.yaml` por falta de permisos
- **THEN** el proceso termina con exit code `3` y un mensaje en stderr que nombra la ruta y el motivo

#### Scenario: Exit code conocido en uso inválido de flag

- **WHEN** `fdh init --agents claude-code --skills +nonexistent --non-interactive` (skill no existe en el catálogo)
- **THEN** el proceso termina con exit code `2` y un mensaje que nombra el skill ausente

#### Scenario: Exit code nuevo no renumera los existentes

- **WHEN** se agrega un exit code nuevo en una release futura
- **THEN** el valor asignado es ≥ 6, y los valores 0-5 + 127 mantienen sus significados originales

### Requirement: Salida JSON es additive-only

Toda salida JSON del CLI (`fdh init --json`, `fdh update --json`, `fdh validate-registry --json`) SHALL evolucionar agregando campos opcionales únicamente. Campos existentes NO SHALL renombrarse, cambiar de tipo, ni cambiar de semántica. Si una evolución incompatible es necesaria, SHALL agregarse un flag `--json-schema=v2` (o equivalente) que coexista con el v1.

#### Scenario: Init JSON conserva campos previos

- **WHEN** se ejecuta `fdh init --skip-doctor --json --non-interactive` en una versión nueva del CLI
- **THEN** la salida JSON contiene los campos existentes (`config_path`, `applied`, `existing`, `doctor_ok`, `doctor_summary`) con los mismos tipos y semántica que en versiones previas

#### Scenario: Init JSON agrega campos opcionales

- **WHEN** una versión nueva agrega `selected_agents`, `selected_skills`, `installed_skills`
- **THEN** esos campos aparecen como opcionales (omisibles cuando no aplican), sin sustituir ni cambiar la forma de los existentes

#### Scenario: Cambio incompatible requiere opt-in explícito

- **WHEN** un cambio futuro necesita renombrar un campo existente
- **THEN** la versión nueva sigue emitiendo el JSON v1 por default y requiere `--json-schema=v2` para optar por la forma nueva

### Requirement: `FDH_PKG_HOST` es contrato de los scripts de install, no del binario

`scripts/install.sh` y `scripts/install.ps1` SHALL leer el host de descarga desde la variable de entorno `FDH_PKG_HOST` (default placeholder `pkg.forge.internal`). El binario `fdh` instalado NO SHALL leer `FDH_PKG_HOST` para resolver el registry o el host de skills; su única dependencia de red SHALL ser el git remote configurado en `registry.url`.

#### Scenario: Script respeta override de env var

- **WHEN** un developer ejecuta `FDH_PKG_HOST=mi.host.real bash install.sh`
- **THEN** todas las URLs resueltas por el script (binario, manifest.json, SHA-256) usan `mi.host.real`; el script no toca `pkg.forge.internal`

#### Scenario: Binario instalado no consulta `FDH_PKG_HOST`

- **WHEN** se ejecuta `FDH_PKG_HOST=cualquier.cosa fdh init --registry-url https://hub.git --skip-doctor --non-interactive`
- **THEN** `fdh init` ignora `FDH_PKG_HOST` y usa exclusivamente `registry.url` para resolver el hub

### Requirement: Marcador `.skill-version` con formato fijo y `content_hash`

Cada copia de skill instalada por `fdh init` SHALL incluir un marcador `.skill-version` (o `.skill-version-<name>` para skills flat tipo Copilot/OpenCode) con los campos `name`, `hub_version`, `hub_commit`, `installed_at` (ISO 8601), `installed_by_fdh` (semver del CLI que escribió el marcador) y `content_hash` (SHA-256 del set de archivos instalados, normalizado con LF). `fdh update` SHALL recomputar `content_hash` al evaluar drift.

#### Scenario: Marcador escrito al instalar

- **WHEN** `fdh init` instala `design-system` en `.claude/skills/design-system/`
- **THEN** el archivo `.claude/skills/design-system/.skill-version` existe y contiene los seis campos poblados

#### Scenario: Drift local detectado por hash

- **WHEN** un developer modifica un archivo dentro de `.claude/skills/design-system/` y luego corre `fdh update`
- **THEN** `fdh update` recomputa `content_hash`, compara contra el valor en `.skill-version`, detecta drift, emite warning y skip — salvo que se haya pasado `--force`

#### Scenario: Hash es estable a través de plataformas (LF normalization)

- **WHEN** un mismo set de archivos se instala en Linux/macOS (LF) y en Windows (CRLF)
- **THEN** el `content_hash` resultante es idéntico (la normalización a LF ocurre antes del SHA-256)

### Requirement: Límites entre packages Go preservados

El código SHALL respetar las siguientes dependencias entre packages:

- `internal/cli/*` PUEDE importar `pkg/hubregistry`, `pkg/adapters`, `pkg/registry`, `pkg/bundle`, `internal/testutil`.
- `pkg/hubregistry` y `pkg/adapters` NO SHALL importar nada de `internal/cli`.
- `pkg/adapters` NO SHALL depender de `pkg/hubregistry` (ambos son consumidos por `internal/cli`; mantenerlos sin acoplamiento permite test aislado).
- `pkg/bundle`, `pkg/portability`, `pkg/provenance` mantienen sus dependencias actuales.

#### Scenario: Importación inversa no compila

- **WHEN** un cambio agrega `import "github.com/forge/fdh/internal/cli"` dentro de `pkg/adapters/`
- **THEN** `go build ./...` falla con un error de ciclo de importación o la regla se valida en CI vía `go vet` o `gochecks`

#### Scenario: Adapters no dependen de hubregistry

- **WHEN** un cambio agrega `import "github.com/forge/fdh/pkg/hubregistry"` dentro de `pkg/adapters/`
- **THEN** la convención requiere refactorizar para inyectar la metadata vía argumento del método, no via import; CI rechaza el cambio si tiene un linter para esta regla

### Requirement: TUI library con detección de TTY y fallback documentado

El wizard de `fdh init` SHALL usar una librería TUI (probablemente `github.com/charmbracelet/huh`) que detecte automáticamente cuándo stdin/stdout no es TTY y SHALL caer en modo no interactivo emitiendo el mensaje "wizard requires a TTY; use --agents / --skills flags or --non-interactive" con exit code `0` (no es error, es información). La elección final de librería SHALL fijarse en `go.mod` al implementar.

#### Scenario: Fallback cuando stdin no es TTY

- **WHEN** `fdh init` se invoca con stdin redirigido (ej.: `echo | fdh init`)
- **THEN** el wizard no intenta dibujar UI, emite el mensaje de uso, y aplica el flujo no-interactivo si las flags lo permiten

#### Scenario: Wizard inicializa correctamente en TTY

- **WHEN** `fdh init` se invoca en una sesión interactiva sin flags de selección
- **THEN** el wizard dibuja los tres steps (agentes, skills, resumen) y procesa input

### Requirement: Cache local del hub bajo XDG/AppData

`fdh init` y `fdh update` SHALL clonar/pullear el hub configurado en `registry.url` a una ubicación cache per-usuario: `$XDG_CACHE_HOME/fdh/hub/` en Linux/macOS (default `~/.cache/fdh/hub/`) o `%LOCALAPPDATA%\fdh\hub\` en Windows. SHALL usar shallow clone (`--depth 1`) + sparse-checkout limitado a `skills/registry.yaml` + `skills/<seleccionado>/` por defecto para minimizar el tamaño del checkout.

#### Scenario: Primer init crea cache

- **WHEN** un developer corre `fdh init` por primera vez
- **THEN** el directorio cache existe, contiene el shallow clone, y `git -C <cache> rev-parse HEAD` devuelve un commit válido

#### Scenario: Init subsecuente hace fetch incremental

- **WHEN** un developer corre `fdh init` por segunda vez
- **THEN** `fdh` hace `git fetch` (no clone fresh), reusa el directorio cache existente

#### Scenario: Cache corrupto se recupera

- **WHEN** el cache existe pero `git fsck` reporta corrupción
- **THEN** `fdh` elimina el cache y re-clona, emitiendo un warning con la causa
