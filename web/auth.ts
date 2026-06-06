/**
 * Auth.js (NextAuth v5) configuration for the FDH portal.
 *
 * The Keycloak provider issues the OIDC code-flow we expect; tokens are
 * stored in HTTP-only signed cookies managed by Auth.js. The Go portal
 * API validates tokens independently against the same Keycloak JWKS, so
 * the frontend and backend agree on identity without sharing session state.
 *
 * NOTE: NextAuth's "keycloak" provider is a generic OIDC client — the name is
 * incidental. The same AUTH_KEYCLOAK_* vars point at whatever OIDC issuer the
 * deployment's IdP profile selects: a self-hosted Keycloak ("local") or a
 * managed provider like Entra ID / Okta / Auth0 ("external"). No code change is
 * needed to switch profiles. See docs/idp-profiles.md.
 *
 * Environment variables consumed at startup:
 *   AUTH_SECRET                — random 32+ byte secret for cookie signing
 *   AUTH_KEYCLOAK_ID           — OIDC client_id registered in Keycloak
 *   AUTH_KEYCLOAK_SECRET       — OIDC client_secret (confidential client)
 *   AUTH_KEYCLOAK_ISSUER       — issuer URL the browser hits + the token
 *                                `iss` claim must equal. Local-dev value:
 *                                http://localhost:18088/realms/fdh-dev
 *   AUTH_KEYCLOAK_WELLKNOWN    — (optional) full discovery URL used for
 *                                server-side fetches when the issuer URL
 *                                is unreachable from the container, e.g.
 *                                http://host.docker.internal:18088/...
 *                                Defaults to {issuer}/.well-known/openid-configuration
 *   NEXT_PUBLIC_SITE_URL       — public origin (used for the redirect_uri)
 */

import NextAuth from "next-auth";
import Keycloak from "next-auth/providers/keycloak";
import { customFetch } from "next-auth";

// Cloudflare compresses the IdP responses with `zstd` (verified) — and may
// also use `br`. Node 22's fetch/undici can't decompress `zstd`, so it throws
// "controller[kState].transformAlgorithm is not a function", crashing Auth.js's
// server-side OIDC token exchange and surfacing as a 502 on /api/auth/callback.
// Force uncompressed responses for the provider's token/jwks/userinfo fetches.
// (The global-fetch patch below is the broader belt-and-suspenders cover that
// also catches the discovery fetch, which does not route through customFetch.)
const noCompressionFetch: typeof fetch = (input, init) => {
  const headers = new Headers(init?.headers);
  headers.set("accept-encoding", "identity");
  return fetch(input, { ...init, headers });
};

const issuer = process.env.AUTH_KEYCLOAK_ISSUER;
// In local-dev with Docker, the container's `localhost` is itself, not the
// host. We allow overriding just the discovery URL so server-side fetches
// can go through `host.docker.internal` while the issuer string stays the
// browser-facing `localhost:18088`. In production both URLs are the same.
const wellKnown =
  process.env.AUTH_KEYCLOAK_WELLKNOWN ??
  (issuer ? `${issuer}/.well-known/openid-configuration` : undefined);

// Cloudflare zstd/brotli-compresses the IdP's responses, and Node 22's
// fetch/undici can't decompress `zstd` — it throws
// "controller[kState].transformAlgorithm is not a function", which crashes
// BOTH the OIDC discovery (sign-in) and the token/jwks exchange (callback).
// The provider-level customFetch below only covers the token/jwks path, not
// discovery, so we also patch the global fetch to force `Accept-Encoding:
// identity` for every request to the IdP host (Cloudflare honors it and
// returns the response uncompressed — verified). Scoped to the IdP host so no
// other server-side fetch is affected.
const idpHost = (() => {
  try {
    return issuer ? new URL(issuer).host : "";
  } catch {
    return "";
  }
})();
if (
  idpHost &&
  typeof globalThis.fetch === "function" &&
  !(globalThis as { __idpFetchPatched?: boolean }).__idpFetchPatched
) {
  const origFetch = globalThis.fetch;
  globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
    try {
      const url =
        typeof input === "string"
          ? input
          : input instanceof URL
            ? input.href
            : (input as Request).url;
      if (url && url.includes(idpHost)) {
        const headers = new Headers(
          init?.headers ?? (input instanceof Request ? input.headers : undefined)
        );
        headers.set("accept-encoding", "identity");
        return origFetch(input, { ...init, headers });
      }
    } catch {
      /* fall through to the original fetch */
    }
    return origFetch(input, init);
  }) as typeof fetch;
  (globalThis as { __idpFetchPatched?: boolean }).__idpFetchPatched = true;
}

export const { auth, handlers, signIn, signOut } = NextAuth({
  trustHost: true,
  session: { strategy: "jwt" },
  providers: [
    Keycloak({
      clientId: process.env.AUTH_KEYCLOAK_ID ?? "fdh-portal",
      clientSecret: process.env.AUTH_KEYCLOAK_SECRET ?? "dev-secret",
      issuer,
      wellKnown,
      // PKCE is enforced by default; we keep it explicit for clarity.
      checks: ["pkce", "state"],
      // Avoid the Node-22 brotli decompression crash on the token exchange.
      [customFetch]: noCompressionFetch,
    }),
  ],
  callbacks: {
    async jwt({ token, account, profile }) {
      // Persist the IdP access token + groups on first sign-in so we can
      // forward to the Go API on outbound calls.
      if (account) {
        token.accessToken = account.access_token;
        token.idToken = account.id_token;
        token.expiresAt = account.expires_at;
      }
      if (profile) {
        // Keycloak puts group memberships under `groups` (or `realm_access.roles`).
        // Capture whatever's there for portal-side role mapping.
        const p = profile as Record<string, unknown>;
        if (Array.isArray(p.groups)) {
          token.groups = p.groups as string[];
        }
        if (typeof p.preferred_username === "string") {
          token.preferredUsername = p.preferred_username;
        }
      }
      return token;
    },
    async session({ session, token }) {
      // Fields below are typed via types/next-auth.d.ts augmentation.
      session.user = {
        ...session.user,
        groups: token.groups ?? [],
        preferredUsername: token.preferredUsername,
      };
      session.accessToken = token.accessToken;
      return session;
    },
  },
  pages: {
    signIn: "/auth/signin",
  },
});
