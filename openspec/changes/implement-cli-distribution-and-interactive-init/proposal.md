## Why

El hub `forge-development-hub` publicó el change `add-fdh-cli-distribution-and-interactive-init` que define el contrato (cuatro specs nuevos: `fdh-cli-distribution`, `fdh-init-interactive`, `fdh-skills-sync`, `hub-skills-registry`) para que un developer pueda hacer `brew install fdh && fdh init` y terminar con sus agentes IA elegidos y las skills "base" copiadas desde el hub a los directorios convencionales de cada agente. Hoy `fdh` cubre parcialmente el flujo (`internal/cli/init.go` configura registry+scope+doctor) pero no es interactivo, no consume skills del hub, y no tiene canal de distribución que setee PATH per OS — el developer baja un tarball y mueve el binario a mano según `docs/quickstart.md`. Este change implementa en Go ese contrato del hub para cerrar el ciclo end-to-end.

## What Changes

- **Wizard interactivo en `fdh init`**: detecta TTY, abre prompts para seleccionar agentes IA detectados (extiende `rc.Adapters.DetectAll`) y skills del catálogo `skills/registry.yaml` del hub (defaults pre-tildados, extras opt-in, opt-out de defaults). Banderas `--agents`, `--skills`, `--no-defaults`, `--dry-run`, `--non-interactive` para CI. La salida JSON existente (`InitResult`) se extiende con campos opcionales (`selected_agents`, `selected_skills`, `installed_skills`) — additive, no breaking.
- **Nuevo subcomando `fdh update`**: recorre directorios de agentes conocidos, encuentra marcadores `.skill-version`, hace pull del hub, calcula diff y aplica con confirmación. Soporta `--yes`, `--dry-run`, `--skill`, `--agent`, `--force` (para sobrescribir edits locales detectados por hash).
- **Nuevo subcomando `fdh validate-registry`**: valida `skills/registry.yaml` del hub (name único kebab-case, paths existentes, no huérfanos, `agents_supported` no vacío, `schema_version` soportado). Reemplaza el placeholder Python (`tools/validate-registry.py`) que el hub usa hoy en CI.
- **Lectura/parseo de `skills/registry.yaml` y consumo de catálogo**: nuevo package `pkg/hubregistry/` que clona/pull el hub vía `registry.url`, parsea el YAML, valida y expone la lista de skills al wizard y a `update`.
- **Adapter de copia per ecosistema**: extiende `pkg/adapters/` para mapear `skills/<name>/` del hub al target convencional de cada agente (copia literal para Claude/Codex; conversión a `<name>.prompt.md` para Copilot; conversión a `commands/<name>.md` para OpenCode). Escribe el marcador `.skill-version` (o `.skill-version-<name>` para skills flat).
- **Pipeline de distribución y scripts de install**: nuevos `scripts/install.sh` y `scripts/install.ps1` que leen `FDH_PKG_HOST` (env var, default placeholder `pkg.forge.internal`), descargan el tarball/zip, verifican SHA-256 contra el manifest, extraen a `$HOME/.fdh/bin/` y agregan al PATH (zshrc/bashrc o HKCU registry). Manifest JSON publicado en `${FDH_PKG_HOST}/fdh/manifest.json`.
- **Formula Homebrew + manifest winget**: artefactos versionados publicados a un tap interno (`forge-internal/tools`, placeholder) y a un winget source interno (placeholder `forge.FDH`). Generados por `goreleaser` o equivalente en el pipeline de CI del repo `fdh`.
- **Manifest publisher**: nuevo job de release que sube tarballs/zips per (os, arch) + SHA-256 + manifest.json al host configurado por `FDH_PKG_HOST` (Artifactory/Nexus/S3/GH Enterprise — decisión del equipo de plataforma).
- **Compat con `fdh init` existente**: el comando sigue aceptando flags y env vars actuales sin cambio de semántica; el wizard sólo se activa cuando stdin es TTY y no se pasaron flags de selección.

## Capabilities

### New Capabilities

- `fdh-cli-implementation-contract`: API interna que el código Go SHALL honrar para satisfacer los 4 specs del hub — exit codes estables, JSON output additive-only, contrato de env vars (`FDH_PKG_HOST`, `FDH_DEFAULT_REGISTRY`), límites entre packages (`internal/cli`, `pkg/hubregistry`, `pkg/adapters`), formato de `.skill-version`. Es una spec de implementación, no de UX (la UX vive en los 4 specs del hub).

### Modified Capabilities

<!-- Los 4 specs del hub (fdh-cli-distribution, fdh-init-interactive, fdh-skills-sync, hub-skills-registry) son la fuente de verdad de comportamiento user-facing y NO se duplican aquí — se referencian. Este change no modifica ninguna capability existente en openspec/specs/ de este repo. -->

## Impact

- **Contrato externo:** los 4 specs del hub `forge-development-hub/openspec/changes/add-fdh-cli-distribution-and-interactive-init/specs/{fdh-cli-distribution,fdh-init-interactive,fdh-skills-sync,hub-skills-registry}/spec.md` son la fuente de verdad de UX y deben estar archivados (sincronizados a `openspec/specs/`) antes de hacer apply de este change.
- **Nuevos packages Go:** `pkg/hubregistry/` (parser + cache del hub), extensión de `pkg/adapters/` (adapters per ecosistema con transformación), extensión de `internal/cli/` con `init.go` (wizard + flags nuevas), `update.go` (subcomando nuevo), `validate-registry.go` (subcomando nuevo).
- **Nuevas dependencias Go:** librería TUI (probable: `github.com/charmbracelet/huh` o `github.com/AlecAivazis/survey/v2`); YAML parser (probable: `gopkg.in/yaml.v3`, ya común). Decisión final en design.md.
- **Nuevos scripts:** `scripts/install.sh`, `scripts/install.ps1` con SHA-256 verification y PATH editing.
- **Nuevos artifacts de release:** formula Homebrew (Ruby), manifest winget (YAML), `.deb`/`.rpm` (vía `nfpm` o equivalente). Pipeline de release probable: `goreleaser`.
- **CI del repo `fdh`:** nuevo job de release matrix-build (linux/macOS/windows × amd64/arm64), un job que publica al host configurado por `FDH_PKG_HOST`, un job que actualiza el manifest. Hoy `.github/workflows/` existe pero no tiene esos jobs.
- **Migración del validator del hub:** cuando este change aterrice, el hub puede deprecar `tools/validate-registry.py` y `.github/workflows/validate-registry.yml` apuntará a `fdh validate-registry` en su lugar (cambio futuro del hub, no incluido aquí).
- **Sin breaking changes:** `fdh init` mantiene flags y comportamiento previos; nuevas flags y subcomandos son additive. El JSON output se extiende sólo con campos opcionales.
- **Distribución bloqueada por plataforma:** los canales reales (tap Homebrew interno, source winget interno, host de `FDH_PKG_HOST`) requieren coordinación con plataforma forge — el spec define el contrato; el rollout depende de esa coordinación.
- **Firma de binarios diferida:** el cert corporativo Authenticode/Apple Developer ID no está garantizado para el primer release; el design del hub ya cubre la trade-off (warning explícito + SHA-256 verification + binarios no firmados aceptables como degradación).
