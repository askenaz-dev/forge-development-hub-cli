# Known issues — dev-portal MVP

Issues we hit during the M9 (docker-compose) bring-up that are workable
around for the pilot but should be addressed before broad rollout.

## 1. Web container build — pnpm strict hoist vs. Next.js 14 styled-jsx

**Symptom:** `pnpm build` inside the web Docker image fails with
`Cannot find module 'styled-jsx/package.json'` or
`Cannot find module '/app/node_modules/next/dist/bin/next'`.

**Root cause:** pnpm's default strict isolation hides `styled-jsx`
(implicitly required by Next.js 14) from the top-level node_modules.
Combined with the `output: "standalone"` build that copies only a
subset of node_modules, the runtime can't resolve the package.

**Workaround in this repo:** `.npmrc` sets `shamefully-hoist=true` so
every dep is hoisted; the host's `pnpm install` produces a lockfile
that works locally. Inside Docker the install runs but the build still
trips because the standalone trace doesn't follow the hoisted package
chain reliably.

**Recommended fix:** switch the web Docker image to npm or yarn for the
install step (both have flatter trees by default), or pin Next.js to a
version with cleaner runtime dependency declarations. Alternatively,
adopt `output: "standalone"` with an explicit `outputFileTracingRoot`
covering the hoisted layout.

**Pilot impact:** local-dev still works — run the web with
`pnpm dev` natively on the host and point at the dockerized API +
Keycloak. The Helm chart's web Deployment also works (it builds on a
clean Kubernetes node, not via pnpm-in-Docker).

## 2. Keycloak issuer URL mismatch between Docker network and host

**Symptom:** API exits at startup with
`oidc: issuer URL provided to client ("http://keycloak:8080/realms/fdh-dev")
did not match the issuer URL returned by provider`.

**Root cause:** Keycloak in compose listens on port 8080 inside the
container (Docker network alias `keycloak:8080`) and on `localhost:18088`
on the host. The issuer claim in tokens is a single string, but the API
reaches Keycloak via the Docker alias while the browser reaches it via
`localhost`. The two URLs differ.

**Workaround:** for local-dev, run the API natively on the host so it
shares the browser's `localhost:18088` view of Keycloak. The dockerized
API works once we set `KC_HOSTNAME=keycloak` + add a `host-gateway`
mapping so the browser can also reach `http://keycloak:8080`, but that
requires editing `/etc/hosts`.

**Recommended fix:** front Keycloak with a reverse proxy that exposes a
single hostname both the Docker network and the host can resolve to —
e.g., a tiny nginx in compose that listens on a non-conflicting port
and the API + browser both target. The Helm chart sidesteps this by
deploying behind a single Ingress already.

## 3. next-intl routing requires `app/[locale]/` segment

**Symptom:** With `next-intl`'s `createMiddleware` enabled, all routes
404 because pages live directly under `app/` (e.g. `app/install/page.tsx`)
instead of `app/[locale]/install/page.tsx`.

**Root cause:** next-intl v4's middleware-based routing rewrites every
request to include the locale prefix, expecting pages under a `[locale]`
dynamic segment. We scaffolded pages without that segment for MVP
simplicity.

**Workaround:** the middleware is currently a passthrough; `i18n.ts`
returns the default locale on every request. The portal renders in
Spanish; the locale switcher is a no-op until the structural refactor
lands.

**Recommended fix:** move every page under `app/[locale]/`, update
`getTranslations` callers as needed, and restore the middleware. ~30
minutes of mechanical file moves.

## 4. Port conflicts on developer machines

**Symptom:** `docker compose up` fails with
`Bind for 0.0.0.0:<port> failed: port is already allocated`.

**Root cause:** Other locally-running projects (in our case
`forge-fabric-keycloak` and `forge-fabric-openfga`) hold ports 8080 and
8088.

**Workaround in this repo:** compose binds non-default ports
(`18088` for Keycloak, `28080` for the API) to dodge common collisions.

**Recommended fix:** document the port matrix prominently in
`docs/local-dev.md` and consider adopting `host-gateway`-style network
mode with internal-only ports + a single Ingress nginx for the host
boundary.

---

None of these is a portal-correctness bug. All four are environmental
plumbing issues with documented workarounds. The recommended fixes are
not in scope for the dev-portal change; spin them off as follow-up
issues once the pilot has flushed out additional real-world gotchas.
