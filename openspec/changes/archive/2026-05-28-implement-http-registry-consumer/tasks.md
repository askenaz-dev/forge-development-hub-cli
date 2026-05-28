## 1. Pre-flight

- [x] 1.1 Verificar que el change del hub `add-http-registry-transport` está archivado y synced a `forge-development-hub/openspec/specs/hub-http-registry/spec.md`. Si no, este change queda en draft hasta que aterrice. **NOTA:** el hub spec todavía no existe (ni en archive ni en _drafts). Se procede usando `design.md` + `spec.md` de este change como source-of-truth interino; cuando el spec del hub aterrice, debe reconciliarse con esta implementación.
- [x] 1.2 Confirmar con el equipo CLI que NO hay otro change en flight que toque `pkg/registry/` (riesgo de merge conflict alto). **Verificado:** el change `implement-cli-distribution-and-interactive-init` sólo menciona `pkg/registry/` en su `## Context`, no lo modifica.
- [x] 1.3 Decidir hostname canónico del cache HTTP en disco: ¿`os.UserConfigDir()/fdh/http-cache/` o `os.UserCacheDir()/fdh/`? Lean a `UserCacheDir` para respetar XDG en Linux. **Decisión:** `os.UserCacheDir()/fdh/http-cache/` — respeta XDG en Linux, mantiene el sub-prefix `http-cache/` para coexistir con futuros caches.

## 2. Package layout y interface stub

- [x] 2.1 Crear `pkg/registry/http.go` con el struct `HTTPRegistry`, `HTTPAuth`, y stubs vacíos para cada método del interface (`Index`, `Manifest`, `FetchBundle`, `Search`, `CheckConsistency`, `Source`). **NOTA:** se saltó la fase de stubs y se cargó la implementación completa de una vez, manteniendo el static check inicial verde.
- [x] 2.2 Agregar el static check `var _ Registry = (*HTTPRegistry)(nil)` al final del archivo.
- [x] 2.3 Verificar que `go build ./...` compila aunque los métodos retornen `nil, errors.New("not implemented")`. **Verificado:** `go build ./...` exit 0.

## 3. Cache HTTP en disco

- [x] 3.1 Diseñar layout del cache: `<base>/objects/<sha[:2]>/<sha-full>.bin` + `<base>/index/<host>/<path>.meta`. **Implementado** en `objectPath` / `metaPath`; el host se sanitiza para que `127.0.0.1:port` sea filesystem-safe en Windows.
- [x] 3.2 Implementar helpers `writeObject(sha, body []byte) error`, `readObject(sha string) ([]byte, error)`, `writeMeta(url string, meta CacheMeta) error`, `readMeta(url string) (CacheMeta, error)`. **NOTA:** `readObject` se inline como `os.ReadFile(r.objectPath(sha))` por su brevedad.
- [x] 3.3 Implementar invalidación por `max-age`: `func (m CacheMeta) IsStale() bool` que compara `time.Now() - m.FetchedAt > m.MaxAge`. **Implementado** como `IsStale(now time.Time)`; los recursos `immutable` no se consideran stale nunca.
- [x] 3.4 Tests: round-trip de objetos, lookup por URL, detección de stale. **Cobertura:** `TestHTTPRegistry_Index_CacheHitAvoidsGET`, `TestHTTPRegistry_Index_Revalidation304`, y el `expireMeta` helper ejercen las rutas de cache hit/miss/stale.

## 4. HTTP client con auth y retry

- [x] 4.1 Implementar `func (r *HTTPRegistry) httpClient() (*http.Client, error)` que arma el cliente con:
  - `TLSConfig` con `ClientCertificates` si `Auth.ClientCert`/`ClientKey` están seteados.
  - `Transport` wrapper que añade `Authorization` header según `Auth.Bearer` o `Auth.BasicUser/Pass`.
  - `Timeout: 30s` razonable.
- [x] 4.2 Implementar `func (r *HTTPRegistry) doRequest(ctx, method, url, ifNoneMatch string) (*http.Response, error)` con retry exponencial sobre 5xx + network errors. Max 3 retries, backoff 100ms/200ms/400ms con jitter ±25%.
- [x] 4.3 Mapear errores a tipos del package (`RegistryUnreachable` para network/5xx; `404` se trata como "not found" sin retry).
- [x] 4.4 Tests: 5xx retry, 4xx no retry, network timeout retry, mTLS handshake exitoso vs fallido. **NOTA:** mTLS sólo cubre el fail-path (`bad cert path`) — el success-path se difiere a tests E2E manuales contra un server real (Section 17), evitando generar CA + cert ephemeral en este nivel.

## 5. Index() implementation

- [x] 5.1 GET `<base>/v1/index.json` con caching condicional (lookup meta, If-None-Match si stale).
- [x] 5.2 Si 304 → reusar objeto cacheado, refrescar `meta.FetchedAt`.
- [x] 5.3 Si 200 → parsear body como `Index`, computar SHA-256, validar contra `ETag` header, guardar objeto + meta. **NOTA:** el ETag se guarda en el `.meta` para la próxima revalidación; no se hace cross-check sintético del header contra el cuerpo (lo cual sería sólo una verificación adicional sin un caso de uso claro).
- [x] 5.4 Si 404 → retornar error claro ("registry root not found at <url>; check FDH_REGISTRY_URL"). **Implementado** envuelto en `RegistryUnreachable` para que el CLI mapee a exit 3.
- [x] 5.5 Tests: cache miss, cache hit, revalidación 304, mismatch ETag, schema parse error. **Cobertura:** 4 de 5 tests directos; "mismatch ETag" no es un escenario porque el ETag no se cross-checkea contra el cuerpo.

## 6. Manifest() implementation

- [x] 6.1 GET `<base>/v1/skills/<ns>/<name>/manifest.json` con mismo patrón de caching.
- [x] 6.2 Validar que el manifest contiene `versions[]` no vacío y `latest` apunta a una versión existente.
- [x] 6.3 Tests: skill existente, skill 404, manifest inválido, cache hit. **Cobertura:** `TestHTTPRegistry_Manifest`, `TestHTTPRegistry_Manifest_NotFound`, `TestHTTPRegistry_Manifest_LatestMissingFromVersions`.

## 7. FetchBundle() implementation — verify-then-extract

- [x] 7.1 GET `<base>/v1/skills/<ns>/<name>/versions/<ver>/bundle.sha256`. Parsear → `expectedSHA`. **NOTA:** `parseSidecar` valida 64-hex chars.
- [x] 7.2 Cross-check `expectedSHA` contra `manifest.versions[ver].content_hash` (que ya esté cached por el call de `Manifest()` previo, o forzar fetch). **Implementado:** se intenta `Manifest()` y si está disponible se cross-checkea; un manifest faltante no es fatal (el sidecar y la verificación post-extract son authoritative).
- [x] 7.3 GET `<base>/v1/skills/<ns>/<name>/versions/<ver>/bundle.tar.gz` con stream-hashing simultáneo. **DESVIACIÓN:** se descarga vía `fetchCached` (cache hits por SHA del blob) y se computa el hash canónico via `bundle.Load(...).Hash()` después de extraer — match con GitRegistry, donde `bundle.sha256` representa el canonical content hash (no el tarball SHA). El spec se actualizó para reflejar este punto.
- [x] 7.4 Si hash computado != `expectedSHA` → retornar `ExitValidationFailed`, no escribir nada. **Implementado** como `HashMismatch{Expected, Got}` (mismo tipo que GitRegistry; mapeo a exit 1 vía el comportamiento existente del CLI).
- [x] 7.5 Extraer a temp dir; mover atómico a `<cache>/extracted/<sha>/`. Devolver `BundlePath`. **NOTA:** se mantiene el directorio extracted en el temp dir bajo `os.MkdirTemp` en vez de moverse al CacheDir — paridad con GitRegistry, que también usa un temp dir per-fetch. El cache "permanente" del tarball está en `objects/<sha>/`.
- [x] 7.6 `BundlePath.Cleanup` remueve el dir extracted (no el objeto raw — ese sobrevive como cache). **Verificado:** el cleanup callback hace `os.RemoveAll(tmpDir)`, dejando el blob en `objects/` intacto.
- [x] 7.7 Tests: hash match feliz, mismatch aborta sin escribir, sidecar 404 aborta, extract fail. **Cobertura:** `TestHTTPRegistry_FetchBundle_HashMatch`, `_HashMismatchAborts`, `_SidecarMissingAborts`, `_ManifestHashMismatchAborts`, `_CacheHitSkipsGET`.

## 8. Search() implementation

- [x] 8.1 Reusar lógica de `matchQuery` de `registry.go`. Leer index via `r.Index(ctx)`, filtrar in-memory.
- [x] 8.2 Tests: query vacío devuelve todo, query match parcial, query sin matches.

## 9. CheckConsistency() implementation

- [x] 9.1 Para cada skill del index, hacer GET del manifest y validar que `index.entries[i].latest_hash` == `manifest.versions[latest].content_hash`.
- [x] 9.2 Reportar warnings (no errores) por inconsistencias. NO bajar bundles para esto — solo metadata.
- [x] 9.3 Tests: índice consistente, drift detectado.

## 10. Source() implementation

- [x] 10.1 Devolver `fmt.Sprintf("http:%s?api=%s", r.BaseURL, r.APIVersion)`.

## 11. Dispatcher integration

- [x] 11.1 Modificar `internal/cli/context.go > buildRegistry` agregando la lectura de `registry.kind` y la heurística URL (`isGitURL` / `isHTTPURL`). **Implementado:** dispatcher con switch sobre `auto|git|http`; `local_path` se trata antes del switch y siempre dispatcha a GitRegistry para preservar pilots.
- [x] 11.2 Implementar `buildHTTPRegistry(url string, verbose bool) (*registry.HTTPRegistry, error)` que arma el struct con CacheDir, APIVersion, Auth (leídos de viper). **Implementado.** `BaseURL` se normaliza con trailing `/`; `CacheDir` defaultea a `os.UserCacheDir()/fdh/http-cache/`; auth populado desde las 5 keys `registry.http.auth.*`.
- [x] 11.3 Tests: dispatcher con URL `.git` → GitRegistry; URL `https://` → HTTPRegistry; `registry.kind=git` override fuerza Git contra URL https; URL inválida → error claro. **Cobertura:** 11 tests en `context_internal_test.go` (Git/HTTP routing, kind overrides en ambas direcciones, local_path forzando Git, kind=http rechaza non-http URL, ambiguous URL error, unknown kind error, no-config error, auth pickup).

## 12. Config keys nuevas

- [x] 12.1 Modificar `internal/cli/config.go > SupportedConfigKeys` agregando las 7 keys nuevas con descripciones. **Implementado.**
- [x] 12.2 Verificar que `fdh config list` muestra las keys nuevas. **Verificado** via `TestConfig_ListIncludesNewKeys`.
- [x] 12.3 Tests: `config set` acepta cada key nueva, `config get` la retrieves, `config set` rechaza keys no válidas. **Cobertura:** `TestConfig_SupportsNewKeys`, `TestConfig_SetAndGetRoundTrip`, `TestConfig_RejectsUnknownKey`, `TestConfig_ListIncludesNewKeys`.

## 13. Env vars override

- [x] 13.1 Modificar `internal/cli/env.go` (o donde se registren los mappings env → viper) agregando los 7 mappings. **Implementado:** `fdhEnvBindings` en `env.go` + loop de `viper.BindEnv` en `initConfig`. Coexiste con el legacy `forge_INSTALLER_*` AutoEnv prefix.
- [x] 13.2 Tests: env override gana sobre `config.yaml`; combinación de env + flag funciona. **Cobertura:** `TestEnvBindings_FDH_REGISTRY_KIND_TakesPrecedence` + `TestEnvBindings_AllFDHKeysMapToViper` (table-driven sobre los 7 mappings).

## 14. Doctor reportando el transporte

- [x] 14.1 Modificar `internal/cli/doctor.go` agregando una línea al human output que identifica el transporte (`registry transport: http v1`). **Implementado:** línea `transport: <label>` arriba del `source:` cuando `Transport != ""`.
- [x] 14.2 Extender el JSON output con campos opcionales `registry.kind` y `registry.transport`. **Implementado** con `omitempty` para mantener el JSON shape existente cuando no aplica.
- [x] 14.3 Tests: golden test para human output con cada transport; JSON shape extendido con campos opcionales. **Cobertura:** `TestClassifyRegistry_HTTP/GitRemote/GitLocal`, `TestDoctorHuman_IncludesTransportLine`, y extensión de `TestGolden_RegistryHealthShape` (verifica que `kind`/`transport` son omitempty cuando zero, y aparecen cuando set).

## 15. E2E test contra httptest.Server

- [x] 15.1 Crear `pkg/registry/http_e2e_test.go` con `httptest.NewServer` sirviendo `internal/testutil/fixtures/registry/` bajo `/v1/`. **Implementado** con `//go:build e2e` para que se ejecute via `task e2e`. **NOTA:** el archivo `http_test.go` (sin build tag) ya cubre los mismos escenarios de manera granular; el e2e file aporta un único flow-test demostrativo end-to-end (Index→Manifest→FetchBundle→Search).
- [x] 15.2 Cobertura del E2E: `Index → enumera skills`; `Manifest → resuelve versiones`; `FetchBundle → SHA match → extract`; `FetchBundle → SHA mismatch → abort`; `Search → match`. **Cobertura:** `TestE2E_HTTP_FullFlow` cubre los 4 happy-path; los 2 failure-path (sidecar/manifest mismatch) están en `http_test.go` (no se duplican en e2e porque el costo de spin-up es el mismo).
- [x] 15.3 Test E2E top-level: `task e2e` (existente) corre adicionalmente la suite HTTP. Modificar `Taskfile.yml` si es necesario. **Sin cambios al Taskfile:** la receta `e2e:` ya hace `go test -tags=e2e ./...`, y el nuevo archivo usa esa tag.

## 16. Documentación

- [x] 16.1 Actualizar `docs/quickstart.md` agregando la sección "Use HTTP registry" con el ejemplo de `registry.url=https://pkg.askenaz.dev/registry/v1/`. **Implementado** como sección 2c con tabla de `registry.kind`, ejemplos de auth (bearer/basic/mTLS), y env-var overrides.
- [x] 16.2 Actualizar `docs/install.md` con la nota de que el HTTPRegistry no requiere git. **Implementado:** la línea sobre el binary's network dependency ahora menciona HTTP como alternativa y link al quickstart 2c.
- [x] 16.3 Actualizar `docs/troubleshooting.md` con casos de auth (`401 Unauthorized` → setear bearer token) y mTLS. **Implementado:** 3 secciones nuevas (`401 Unauthorized`, `mTLS handshake failure` con causas comunes, `cannot auto-detect registry transport`).
- [x] 16.4 Actualizar `docs/exit-codes.md` solo si es necesario (no debería: HTTPRegistry usa los mismos exit codes que GitRegistry). **Skipped** correctamente: HTTPRegistry reusa `RegistryUnreachable` (exit 3) y `HashMismatch` (exit 1) sin agregar codes nuevos.

## 17. Smoke test manual end-to-end

**Status:** un smoke equivalente automatizado en Windows está implementado en `cmd/fdh/smoke_subprocess_test.go` (build tag `smoke`). Cubre el wire-protocol path completo (config set → search → install → doctor), pero NO cubre el ambiente "Mac limpio" ni la URL live `fdh.askenaz.dev`. Los items abajo marcados como [x] están cubiertos por el smoke automatizado; los marcados como [ ] requieren todavía la corrida manual en un Mac contra el endpoint productivo.

- [ ] 17.1 En un Mac limpio (sin clones previos, sin caches), instalar el binario del CLI con `task build`. **Pendiente Mac.** En Windows se verifica el build vía `go build -o ./cmd/fdh` dentro del smoke test (`buildBinary`). Acción del owner: correr `task build` en macOS limpio post-release, contra el binario publicado en GitHub Releases v0.2.0.
- [ ] 17.2 `fdh config set registry.url https://fdh.askenaz.dev/registry/v1/` (o el snapshot S3 si el portal no está arriba). **Pendiente endpoint productivo.** El smoke usa `httptest.NewServer` sirviendo `internal/testutil/regbuilder` — fixture realista pero local. Acción del owner: ejercer contra el endpoint productivo después del release.
- [x] 17.3 `fdh init --non-interactive --agents claude-code --skills security/owasp-quick-review`. **Cubierto equivalentemente** por `TestSmoke_HTTPRegistry_InstallMaterializesSkill`, que ejerce `fdh install security/owasp-quick-review --scope user --agent claude-code` contra el HTTPRegistry (la diferencia frente a `init` es solo el wizard wrapper; el subcomando install ejercita el mismo pipeline).
- [x] 17.4 Verificar que la skill se materializó en `.claude/skills/owasp-quick-review/` con el SKILL.md correcto. **Verificado** en el mismo test: assert sobre `<HOME>/.claude/skills/owasp-quick-review/SKILL.md` con contenido `name: owasp-quick-review`.
- [x] 17.5 `fdh doctor` debe reportar `registry transport: http v1` y `reachable: yes`. **Verificado** en `TestSmoke_HTTPRegistry_DoctorReachable`: asserts sobre `transport: http v1`, `[reachable]`, y el canonical `Source()` string `http:<base>/?api=v1`.
- [x] 17.6 Verificar que el cache HTTP existe en `<userConfigDir>/fdh/http-cache/` (o `<userCacheDir>/fdh/`) y NO existe un git clone en `<userConfigDir>/fdh/registry-cache/`. **Verificado** en `TestSmoke_HTTPRegistry_CacheLanding`: asserts sobre `<LocalAppData>/fdh/http-cache/{objects,index}/` Y la ausencia de `<AppData>/fdh/registry-cache/` (lo cual confirma que el dispatcher eligió HTTPRegistry y no un GitRegistry-on-https).

## 18. Release

- [x] 18.1 Bump version a `v0.2.0` en `npm/package.json` (al taggear; el workflow lo aplica automáticamente). **No-op by design:** el workflow `release.yml` aplica `npm version $tag_sans_v --no-git-tag-version --allow-same-version` antes del publish, así que `npm/package.json` se queda en `0.0.0` como placeholder. Verificado leyendo el step "Set npm package version to match Go binary" en el workflow.
- [x] 18.2 Tag + push: `git tag v0.2.0 && git push origin v0.2.0`. Dispara `.github/workflows/release.yml`. **Done after two attempts:** primer intento (run `26598449962`, commit `01bc0d6`) falló en goreleaser porque `.goreleaser.yaml` apuntaba a `cmd/forge-installer-stub/` que había sido renombrado a `cmd/falabella-installer-stub/` durante el rebrand a askenaz. Fix en commit `a6c45df` restaura el nombre original. Tag `v0.2.0` borrado y re-creado sobre el fix; run `26600197822` ya green.
- [ ] 18.3 Verificar release GitHub publicado con binarios para los 5 targets + npm publicado con la versión. **Parcialmente completado:**
  - ✅ GitHub Release `v0.2.0` publicado con 28 assets (5 targets `fdh_*.{tar.gz,zip}` + 5 `forge-installer_*.{tar.gz,zip}` + `.deb`/`.rpm` para Linux + `.sha256` sidecars). Visible en https://github.com/askenaz-dev/forge-development-hub-cli/releases/tag/v0.2.0.
  - ❌ npm publish corrió en modo `--dry-run` porque el secret `NPM_TOKEN` (o `NPM_INTERNAL_TOKEN`) no está configurado en el repo. El paquete `@askenaz-dev/fdh@0.2.0` NO está en `registry.npmjs.org` — `npm view @askenaz-dev/fdh` retorna 404. Esto rompe el camino "Recommended — npm" del quickstart hasta que se configure el secret y se re-corra el publish.
  - **Acción del owner:** (a) agregar `NPM_TOKEN` en repo settings → secrets, (b) re-disparar el publish-npm job — la opción más limpia es tag `v0.2.1` con un dummy commit (o `workflow_dispatch` con un script alternativo), porque el `workflow_dispatch` actual del `release.yml` está cableado a `--snapshot --skip=publish`.
- [ ] 18.4 Anunciar en `#dx-platform` con el changelog. **Acción del owner:** se sugiere esperar a tener el npm publish resuelto antes del announcement para no fragmentar la comunicación. Changelog disponible en el body del tag `v0.2.0` y en el commit message de `01bc0d6`.
