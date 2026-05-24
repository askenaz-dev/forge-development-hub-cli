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

## 2. Keycloak issuer URL mismatch between Docker network and host — RESOLVED

**Symptom (was):** API and/or Auth.js failed with
`oidc: issuer URL provided to client did not match the issuer URL returned by provider`
or `TypeError: fetch failed` during the OIDC flow.

**Root cause (was):** Keycloak's token issuer claim is a single string,
but Docker containers can't reach `localhost:<port>` of the host (their
localhost is themselves) while the browser can.

**Resolution:** the compose stack now uses `host.docker.internal:18088`
as the single Keycloak URL everywhere:
- Browser reaches it because Docker Desktop adds `host.docker.internal`
  to the host's hosts file pointing at `127.0.0.1`.
- Containers reach it via the `host-gateway` magic alias declared in
  the compose `extra_hosts` field (works on Linux Docker too).
- The issuer claim in tokens is `http://host.docker.internal:18088/...`
  — identical from both contexts, so validation passes.

In production the issue doesn't apply: a real Keycloak (e.g.
`keycloak.forge.internal`) is reachable from both browsers and
in-cluster pods at the same DNS name.

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
