import "server-only";

/**
 * BFF (Backend-For-Frontend) service-token helper — SERVER ONLY.
 *
 * The `import "server-only"` guard above makes this module a build-time error
 * if it is ever imported into a Client Component. That is the load-bearing
 * safety property of this file: the Keycloak client secret and the minted
 * service token MUST never reach the browser bundle, `document.cookie`, or any
 * client-visible response.
 *
 * ── Why a service token at all ─────────────────────────────────────────────
 * `web/auth.ts` deliberately omits the IdP `access_token`/`id_token` from the
 * NextAuth session cookie (storing them pushed the JWE cookie past 4 KB →
 * Auth.js chunked it → the OIDC callback's oversized `Set-Cookie` block made
 * Cloudflare return 502). Consequence: the web holds NO user bearer to forward
 * to admin-gated API endpoints. Instead, for privileged server-side API calls
 * (`GET /api/v1/admin/activation`, `POST /api/v1/refresh`, the admin
 * contributions derivation) the web mints a Keycloak **client-credentials**
 * token from the portal's existing confidential client (`AUTH_KEYCLOAK_ID` +
 * `AUTH_KEYCLOAK_SECRET`, already mounted in the web pod) and passes it as the
 * `Authorization: Bearer`.
 *
 * ── Trust model (per design.md D1 + the spec) ──────────────────────────────
 * The web's `session.user.groups` / `resolvePortalRole()` check is ADVISORY
 * UX-gating: it decides what to render. It is NOT the security boundary. The Go
 * API independently validates THIS service token against the same Keycloak
 * JWKS used for user tokens and enforces the required portal role on every
 * privileged call (403 otherwise). The service principal earns the `admin`
 * role via a role-map entry on a DEDICATED service-account group
 * (`fdh-portal-svc`), never the human `fdh-admins` group, so a forged web
 * request cannot bypass the API gate and the service principal is auditable.
 *
 * A leaked client secret is bounded: the only operations the token authorizes
 * in Phase 1 are read-activation and refresh — non-destructive, on
 * derived/ephemeral data. No CONFIG write path exists until Phase 3.
 */

import { patchGlobalFetch } from "./fetch-no-compress";

// Belt-and-suspenders: instrumentation.ts installs the no-compress shim at
// process startup, but call it again here (idempotent) so the client-credentials
// fetch is covered even if this module is the first thing a server path touches.
patchGlobalFetch();

/**
 * A minted service token plus the absolute epoch-ms at which we should stop
 * using it. We refresh slightly BEFORE the real `exp` (see SKEW_MS) so an
 * in-flight API call never carries a token that expires mid-request.
 */
interface CachedToken {
  token: string;
  /** epoch milliseconds after which this token must not be reused */
  notAfterMs: number;
}

/**
 * Module-scoped cache. Module scope on the server means one instance per
 * server worker — never serialized to the client. The token here is a SECRET;
 * it is never exported, logged, or returned to any client-facing code path.
 */
let cached: CachedToken | null = null;

/**
 * Refresh this many ms before the token's real expiry, so a token handed to an
 * API call is comfortably valid for the duration of that call.
 */
const SKEW_MS = 30_000;

/** Shape of the OIDC token endpoint's client-credentials response. */
interface TokenResponse {
  access_token?: string;
  expires_in?: number;
  token_type?: string;
}

/**
 * Derive the Keycloak token endpoint from the configured issuer. The issuer is
 * the same value `auth.ts` consumes (`AUTH_KEYCLOAK_ISSUER`); the standard
 * OIDC token endpoint is `<issuer>/protocol/openid-connect/token`.
 */
function tokenEndpoint(): string {
  const issuer = process.env.AUTH_KEYCLOAK_ISSUER;
  if (!issuer) {
    throw new Error(
      "bff: AUTH_KEYCLOAK_ISSUER is not set; cannot derive the Keycloak token endpoint"
    );
  }
  // Trim a trailing slash so we never produce a `//protocol` path.
  const base = issuer.replace(/\/+$/, "");
  return `${base}/protocol/openid-connect/token`;
}

/**
 * getServiceToken returns a valid Keycloak client-credentials access token for
 * server-side, admin-gated API calls, minting a fresh one only when the cache
 * is empty or about to expire.
 *
 * The token is a bearer SECRET: callers pass it as `Authorization: Bearer` to
 * the in-cluster API and must never surface it to the browser. This function
 * never logs the token or the client secret.
 */
export async function getServiceToken(): Promise<string> {
  const now = Date.now();
  if (cached && now < cached.notAfterMs) {
    return cached.token;
  }

  const clientId = process.env.AUTH_KEYCLOAK_ID;
  const clientSecret = process.env.AUTH_KEYCLOAK_SECRET;
  if (!clientId || !clientSecret) {
    throw new Error(
      "bff: AUTH_KEYCLOAK_ID / AUTH_KEYCLOAK_SECRET are required to mint a service token"
    );
  }

  const body = new URLSearchParams({
    grant_type: "client_credentials",
    client_id: clientId,
    client_secret: clientSecret,
  });

  // Mirror auth.ts's OIDC fetch posture:
  //   - `accept-encoding: identity` dodges the Node-22 zstd decompression crash
  //     ("transformAlgorithm is not a function") on CDN-fronted IdP responses.
  //   - `cache: "no-store"` keeps the token request out of Next.js's fetch
  //     cache (which would otherwise revalidate via an undici call that ignores
  //     the identity header and crashes on a zstd body). Tokens must never be
  //     cached anyway.
  const res = await fetch(tokenEndpoint(), {
    method: "POST",
    headers: {
      "content-type": "application/x-www-form-urlencoded",
      "accept-encoding": "identity",
      accept: "application/json",
    },
    body,
    cache: "no-store",
  });

  if (!res.ok) {
    // Surface the status, never the request body (it carries the client secret)
    // and never the response body verbatim (it may echo sensitive detail).
    throw new Error(
      `bff: client-credentials token request failed with HTTP ${res.status}`
    );
  }

  const data = (await res.json()) as TokenResponse;
  if (!data.access_token) {
    throw new Error("bff: token endpoint response did not include an access_token");
  }

  // `expires_in` is seconds; default to a conservative 60s if the IdP omits it.
  const expiresInMs = (data.expires_in ?? 60) * 1000;
  // Never let SKEW push the lifetime to zero/negative for very short-lived
  // tokens: keep at least a 5s usable window.
  const lifetimeMs = Math.max(expiresInMs - SKEW_MS, 5_000);

  cached = {
    token: data.access_token,
    notAfterMs: Date.now() + lifetimeMs,
  };
  return cached.token;
}

/**
 * Test-only hook to clear the module-scoped token cache between cases. Not part
 * of the production API surface; safe because it only drops the cached token
 * (forcing the next call to re-mint), never exposes it.
 */
export function __resetServiceTokenCacheForTests(): void {
  cached = null;
}
