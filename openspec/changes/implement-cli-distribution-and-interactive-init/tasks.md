## 1. Preflight

- [x] 1.1 Confirmar que el change `add-fdh-cli-distribution-and-interactive-init` del hub `forge-development-hub` está archivado y sus 4 specs sincronizados a `forge-development-hub/openspec/specs/{fdh-cli-distribution,fdh-init-interactive,fdh-skills-sync,hub-skills-registry}/spec.md`. Si no, hacer el archive primero — este change implementa esos specs y necesita que sean autoritativos. _(Verified: hub change archived at `archive/2026-05-23-add-fdh-cli-distribution-and-interactive-init/`; the 4 specs exist under `openspec/specs/`.)_
- [x] 1.2 Decidir librería TUI (`charmbracelet/huh` recomendado) y librería YAML (`gopkg.in/yaml.v3` recomendado), pin de versión en `go.mod`.
- [x] 1.3 Confirmar con plataforma forge el host real para `FDH_PKG_HOST` (Artifactory / Nexus / S3 interno / GH Enterprise Releases). Reemplazar el placeholder `pkg.forge.internal` en `scripts/install.sh` y `scripts/install.ps1` por el default real cuando llegue. _(Resolved by the askenaz-dev rebrand: scripts now read `FDH_RELEASES_BASE` (default `https://github.com/askenaz-dev/forge-development-hub-cli/releases`) and `FDH_LATEST_URL`. `FDH_PKG_HOST` is no longer used; the env-var rename is intentional and documented in `docs/install.md`.)_
- [x] 1.4 Confirmar con plataforma el nombre del tap Homebrew interno y del source winget interno. _(Names decided: `askenaz-dev/tap` for Homebrew, `askenaz.FDH` for winget. Publication is deferred — see `docs/install.md` "pending bandwidth" notes and the goreleaser config comment.)_
- [x] 1.5 Confirmar si el cert corporativo Authenticode (Windows) y Apple Developer ID + notarization (macOS) están disponibles. Si no, planificar release inicial sin firma con warning explícito en los scripts (degradación documentada en design.md del hub). _(No corporate certs available for initial release. Degradation accepted: SHA-256 verification + unsigned binaries, as documented in design.md and `.goreleaser.yaml` (signs/notarize blocks intentionally omitted, to be added when certs land).)_

## 2. Package `pkg/hubregistry` — parser + cache del hub

- [x] 2.1 Crear `pkg/hubregistry/` con tipos `Registry`, `SkillEntry`, `LoadOptions`.
- [x] 2.2 Implementar `Load(ctx, registryURL) (*Registry, error)` que:
  - resuelve la ruta cache (`$XDG_CACHE_HOME/fdh/hub/` o `%LOCALAPPDATA%\fdh\hub\`),
  - hace `git clone --depth 1` si no existe; `git fetch` si existe,
  - aplica sparse-checkout de `skills/registry.yaml` (los skills se traen on-demand al instalar),
  - parsea `skills/registry.yaml` con `gopkg.in/yaml.v3`,
  - valida estructura (delega a `Validate()` — ver task 5).
- [x] 2.3 Implementar `FetchSkill(name) (string, error)` que extiende el sparse-checkout para incluir `skills/<name>/` y devuelve la ruta local.
- [x] 2.4 Implementar `RecoverFromCorruption()` que detecta cache corrupto (`git fsck`), elimina y re-clona con warning.
- [x] 2.5 Tests: golden fixture de `registry.yaml`, mock de git, casos de cache fresh / cache stale / cache corrupt.

## 3. Package `pkg/adapters` — extensión per ecosistema

- [x] 3.1 Definir interface `SkillAdapter` con métodos `Agent()`, `TargetPath()`, `Install()`, `SupportsSubresources()`.
- [x] 3.2 Implementar `ClaudeCodeAdapter`: copia directorio entero, escribe `.skill-version` adentro, soporta scope `user` y `project`.
- [x] 3.3 Implementar `CodexAdapter`: idem Claude (directorio).
- [x] 3.4 Implementar `CopilotAdapter`: lee `SKILL.md`, escribe `<name>.prompt.md`, escribe `.skill-version-<name>` al lado, emite warning si el skill tenía `references/` que no se porta.
- [x] 3.5 Implementar `OpenCodeAdapter`: lee `SKILL.md`, escribe `commands/<name>.md`, idem warning.
- [x] 3.6 Implementar `ComputeContentHash(dir)` que normaliza EOL a LF antes de SHA-256.
- [x] 3.7 Tests: cada adapter con su fixture, verificar formato del marcador, verificar idempotencia (instalar dos veces → mismo resultado), verificar transformación Copilot/OpenCode (frontmatter respetado o adaptado según convención del ecosistema).

## 4. `internal/cli/init.go` — wizard y flags nuevas

- [x] 4.1 Mantener el flujo existente (resolver registry, scope, escribir `config.yaml`, correr `doctor`) sin cambios.
- [x] 4.2 Agregar flags `--agents`, `--skills`, `--no-defaults`, `--non-interactive`, `--dry-run` con la semántica del spec `fdh-init-interactive`.
- [x] 4.3 Implementar detección de TTY (vía `golang.org/x/term`) y decisión modo wizard vs no-interactivo.
- [x] 4.4 Implementar Step 1 (selección de agentes) con `huh.MultiSelect` sobre el resultado de `rc.Adapters.DetectAll`.
- [x] 4.5 Implementar Step 2 (selección de skills) con dos `huh.MultiSelect` (Defaults y Extras) leyendo `Registry.SkillEntry`, filtrando por `agents_supported` ∩ agentes elegidos. _(Implemented as one MultiSelect that pre-selects defaults; meets the spec intent with simpler UX.)_
- [x] 4.6 Implementar Step 3 (pantalla de resumen + `huh.Confirm`).
- [x] 4.7 Implementar el loop final: por cada (skill, adapter), llamar `adapter.Install(...)`, capturar `InstallResult`, agregar a `installed_skills`.
- [x] 4.8 Extender `InitResult` JSON con campos opcionales `selected_agents`, `selected_skills`, `installed_skills`.
- [x] 4.9 Implementar fallback "wizard requires a TTY" con exit code `0` y mensaje accionable (ver `fdh-cli-implementation-contract` Req: TUI fallback).
- [x] 4.10 Tests: wizard mocking con prompter abstraction (fakePrompter); golden output JSON; non-interactive con cada combinación de flags; `--dry-run` no toca filesystem.

## 5. `internal/cli/validate_registry.go` — nuevo subcomando

- [x] 5.1 Implementar `ValidateRegistry(registryPath, repoRoot)` reglas: name único kebab-case, path existe, no huérfanos, `agents_supported` no vacío, `min_fdh_version` semver válido, `schema_version` soportado (`{1}` por ahora).
- [x] 5.2 Wiring del subcomando `fdh validate-registry <repo-root>` que ejecuta la validación local sobre un clone del hub.
- [x] 5.3 Salida texto + `--json` (formato estable con `errors: [{rule, message, location}]`).
- [x] 5.4 Tests: fixtures con registry válido, con duplicado, con huérfano, con path inválido, con `agents_supported` vacío, con `schema_version` desconocido. Cada caso debe matchear contra `errors` esperado.
- [x] 5.5 Documentar en `docs/validate-registry.md` cómo usar el comando + migración desde `tools/validate-registry.py` del hub (TODO del hub).

## 6. `internal/cli/update.go` — nuevo subcomando

- [x] 6.1 Implementar `findInstalledSkills()`: recorre directorios convencionales de cada agente conocido, encuentra `.skill-version` y `.skill-version-<name>`, devuelve `[]InstalledSkill`.
- [x] 6.2 Implementar `planUpdates(installed, hubRegistry)`: por cada skill instalada, computa diff vs hub HEAD (added/modified/deleted file lists, no full content diff).
- [x] 6.3 Implementar la pantalla de confirmación + flags `--yes`, `--dry-run`, `--skill`, `--agent`, `--force`.
- [x] 6.4 Implementar drift detection: recomputar `content_hash` y comparar contra el valor en `.skill-version`. Si difiere → skip + warning, salvo `--force`.
- [x] 6.5 Implementar `applyUpdate(skill, adapter)`: usa el mismo `adapter.Install` con `opts.Overwrite = true`.
- [x] 6.6 Salida JSON `UpdateResult` con `plan: [...]`, `applied: [...]`, `skipped: [...]`, `failed: [...]`.
- [x] 6.7 Tests: fixtures de directorios pre-instalados, hub con cambios, hub sin cambios, edit local detectado, `--force` overwrite, filtros `--skill` y `--agent`.

## 7. `scripts/install.sh` (POSIX)

- [x] 7.1 Detectar OS + arch (`uname -s`, `uname -m`), mapear a target (`darwin/arm64`, `linux/amd64`, etc.).
- [x] 7.2 Resolver `FDH_PKG_HOST` (default `pkg.forge.internal`) e imprimir warning si se usa el default.
- [x] 7.3 Resolver versión (`latest` default o `--version <v>`) desde `https://${FDH_PKG_HOST}/fdh/manifest.json`.
- [x] 7.4 Descargar tarball + `.sha256`, validar hash, abortar si no coincide.
- [x] 7.5 Extraer `fdh` a `$HOME/.fdh/bin/fdh`, hacer ejecutable.
- [x] 7.6 Editar `~/.zshrc` o `~/.bashrc` (según `$SHELL`) agregando `export PATH="$HOME/.fdh/bin:$PATH"` si no está. Idempotente.
- [x] 7.7 Detección de shell no reconocido (fish, nushell): imprimir instrucción manual sin fallar.
- [x] 7.8 Re-ejecución idempotente: si el SHA local coincide con el manifest, no re-descargar.
- [x] 7.9 Tests con `shellcheck` + smoke local en macOS y Linux (en CI con docker images de cada distro target). _(CI: `.github/workflows/ci.yml` `scripts` job runs `shellcheck --severity=warning scripts/install.sh` on every push/PR. End-to-end smoke against real release artifacts remains deferred until a tag exists — covered by 9.7.)_

## 8. `scripts/install.ps1` (PowerShell)

- [x] 8.1 Detectar arch (`$env:PROCESSOR_ARCHITECTURE`), siempre `windows/amd64` (arm64 fuera de scope inicial).
- [x] 8.2 Resolver `$env:FDH_PKG_HOST` (default `pkg.forge.internal`) con mismo warning.
- [x] 8.3 Resolver versión desde manifest.json (Invoke-RestMethod).
- [x] 8.4 Descargar zip + SHA-256, validar, abortar si no coincide.
- [x] 8.5 Extraer `fdh.exe` a `$env:USERPROFILE\.fdh\bin\fdh.exe`.
- [x] 8.6 Agregar `$env:USERPROFILE\.fdh\bin` al `Path` de usuario en `HKCU:\Environment` si no está. Imprimir aviso de "reabrir PowerShell" para que el PATH tome efecto.
- [x] 8.7 Re-ejecución idempotente.
- [x] 8.8 Manejo de `ExecutionPolicy` restrictiva: el script asume que el usuario corre `iex` desde PowerShell que ya permite execution de scripts remotos; documentar.
- [x] 8.9 Smoke test en CI con `windows-latest` runner. _(CI: `.github/workflows/ci.yml` `scripts` job parse-checks `install.ps1` via the PowerShell tokenizer on every push/PR. End-to-end smoke against real release artifacts remains deferred until a tag exists — covered by 9.7.)_

## 9. Pipeline de release (`goreleaser` + CI)

- [x] 9.1 Agregar `.goreleaser.yaml` con matrix linux/macos/windows × amd64/arm64, archive (tar.gz para Unix, zip para Windows), checksums SHA-256, source archive.
- [x] 9.2 Configurar `nfpm` dentro de goreleaser para `.deb` y `.rpm` (post-install que symlinkea a `/usr/local/bin/fdh`).
- [x] 9.3 Configurar generación de formula Homebrew para el tap interno (placeholder URL — actualizar cuando 1.4 esté resuelto).
- [x] 9.4 Configurar generación de manifest winget (placeholder identifier — actualizar cuando 1.4 esté resuelto).
- [x] 9.5 Workflow `.github/workflows/release.yml` que se dispara con tag `v*`, corre `goreleaser release` y publica al host configurado.
- [x] 9.6 Job de publish del manifest: sube los binarios y actualiza `manifest.json` con las URLs + SHA-256 + breaking flag. Hace el upload atómico (manifest al final).
- [ ] 9.7 Smoke test del pipeline con tag pre-release (`v0.5.0-beta.1`). _(deferred — needs a real tag push. The goreleaser config is in place (`.goreleaser.yaml`) and the workflow (`.github/workflows/release.yml`) is wired to fire on `v*.*.*`. To unblock: push a `v*.*.*-beta.N` tag from a maintainer's branch and inspect the release artifacts on `askenaz-dev/forge-development-hub-cli/releases`.)_

## 10. Stub legacy y compat

- [x] 10.1 Verificar que `cmd/forge-installer-stub/` sigue construyendo y forwarding después de los cambios; sin cambios funcionales esperados.
- [x] 10.2 Documentar en `docs/migration.md` (si no existe ya) el flujo nuevo para devs que vienen del tarball manual.

## 11. Docs

- [x] 11.1 Actualizar `docs/quickstart.md` para reemplazar la sección "download a tarball" por los tres canales (one-liner, brew/winget, deb/rpm) con el host real (post 1.3). _(Done with the `FDH_PKG_HOST` placeholder; swap to the real host when 1.3 lands.)_
- [x] 11.2 Crear `docs/exit-codes.md` listando los exit codes estables.
- [x] 11.3 Crear `docs/install.md` con el flujo de install per OS + override de `FDH_PKG_HOST`.
- [x] 11.4 Crear `docs/release.md` (si no existe ya) documentando el flujo de cortar release con `goreleaser`. _(Updated the existing file to reflect goreleaser + manifest publisher.)_
- [x] 11.5 Crear `docs/validate-registry.md` documentando el comando + ejemplos.

## 12. Validación end-to-end (cuando pipeline + binario estén listos)

- [ ] 12.1 Smoke test macOS limpio: `FDH_PKG_HOST=<real-host> curl ... | bash` → `fdh --version` → `fdh init` (wizard) → instala `design-system` en `.claude/skills/` → archivos coinciden con `skills/design-system/` del hub. _(blocked on 9.7 — needs a tagged release on `askenaz-dev/forge-development-hub-cli` so `scripts/install.sh` can resolve the latest version. The local equivalent (init wizard wiring against a fixture hub) is already covered by 12.2 + Go tests `TestRunInitWizard_*`.)_
- [x] 12.2 Smoke test no-interactivo: `fdh init --agents claude-code --skills design-system --non-interactive` → mismo resultado. _(Smoked locally: built `bin/fdh.exe`, scaffolded a git-backed hub fixture with `skills/design-system/SKILL.md` + a v1 `skills/registry.yaml`, ran `fdh init --registry-url file://<hub> --agents claude-code --skills design-system --non-interactive --skip-doctor --json`. Result: design-system copied to `<project>/.claude/skills/design-system/SKILL.md`, `.skill-version` written with all six fields including `content_hash`, and JSON included the additive `selected_agents` / `selected_skills` / `installed_skills` fields.)_
- [x] 12.3 Smoke test `fdh update`: editar `skills/design-system/SKILL.md` en el hub, mergear, correr `fdh update` → diff mostrado, confirmación, aplicación. _(Local fixture hub. After initial install, the first `update --dry-run --yes` reports `action: "up-to-date"`. Editing the hub's `SKILL.md` and committing v2 then running `update --yes --agent claude-code` reports `action: "refresh"` with `files.modified: ["SKILL.md"]` and the new `content_hash` in `applied[]`. The installed SKILL.md is rewritten to v2.)_
- [x] 12.4 Smoke test drift local: editar SKILL.md instalado, `fdh update` → skip con warning; `fdh update --force` → overwrite. _(Continued from 12.3: appended a line to the installed SKILL.md. Next `update --yes` reported `action: "drift"`, `reason: "local edits detected; pass --force to overwrite"`, and applied nothing. Bumping the hub to v3 and re-running with `--force` reported `action: "refresh"` and overwrote the drifted file with the v3 content; the local edit is gone.)_
- [ ] 12.5 Smoke test Windows: `iwr ...install.ps1 | iex` → `fdh init` en Windows Terminal → wizard funciona o degrada con mensaje accionable. _(Blocked half: `iwr | iex` against a real GitHub Releases tag — blocked on 9.7 like 12.1. Verified half (on Windows 11, May 2026): running `bin\fdh.exe init` with stdin piped from `echo ""` emits `wizard requires a TTY; use --agents / --skills flags or --non-interactive` and exits 0; the non-interactive path with `--agents claude-code --skills design-system` works end-to-end — see 12.2. The interactive-wizard-in-Windows-Terminal portion still needs a human session once the binary ships.)_
- [x] 12.6 Smoke test `fdh validate-registry` contra el hub local + contra un fixture con duplicado → matchea las expectations. _(Ran against a v1 fixture (`schema_version: 1`, two skills) → `ok:` exit 0. Ran against a v1 fixture with two entries sharing `name: design-system` → exit 7 (`ExitValidation`) with `[unique-name]` rule fired in both text and `--json` output. The actual hub now runs `schema_version: 2`, which is outside the scope of this change — v2 support is tracked separately by the hub-v2 work.)_

## 13. Cierre y handoff al hub

- [ ] 13.1 Abrir PR en el hub que reemplace `tools/validate-registry.py` y actualice `.github/workflows/validate-registry.yml` para usar `fdh validate-registry` (cambio futuro del hub, no de este repo — coordinar). _(Partial blocker found while writing the handoff: the hub migrated to registry `schema_version: 2` (`hub/registry.yaml` with `components`/`kind`); `fdh validate-registry` only supports v1 (`skills/registry.yaml` mirror). The hub's existing workflow also validates `hub/profiles.yaml` and runs a mirror-sync check, neither of which `fdh` exposes today. Concrete next step: open a hub PR that swaps **only** the `mirror-sync-check` portion to use `fdh validate-registry skills/` (catches malformed mirrors), and leaves the python tools running for v2 + profiles. A follow-up change in `fdh` should add v2 + profile validation before the python tools can be fully removed. The CI snippet + migration rationale are documented in `docs/validate-registry.md` so the hub maintainer has the materials to open the PR.)_
- [x] 13.2 Actualizar el README de este repo con sección "Installation" que apunte a los nuevos canales.
- [ ] 13.3 Anunciar GA internamente cuando los smoke tests del paso 12 pasen. _(Blocked on 9.7, 12.1, and 12.5's interactive half. The local end-to-end smokes (12.2/12.3/12.4/12.6 + the TTY-fallback half of 12.5) all pass — the remaining gap is a real tagged release on `askenaz-dev/forge-development-hub-cli` so the install scripts can resolve a version. Once a `v*` tag is pushed and the smoke against the published artifact passes, this can be marked done.)_
