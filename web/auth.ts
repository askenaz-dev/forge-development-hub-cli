/**
 * Auth.js (NextAuth v5) configuration for the FDH portal.
 *
 * The Keycloak provider issues the OIDC code-flow we expect; tokens are
 * stored in HTTP-only signed cookies managed by Auth.js. The Go portal
 * API validates tokens independently against the same Keycloak JWKS, so
 * the frontend and backend agree on identity without sharing session state.
 *
 * Environment variables consumed at startup:
 *   AUTH_SECRET            — random 32+ byte secret for cookie signing
 *   AUTH_KEYCLOAK_ID       — OIDC client_id registered in Keycloak
 *   AUTH_KEYCLOAK_SECRET   — OIDC client_secret (confidential client)
 *   AUTH_KEYCLOAK_ISSUER   — full discovery URL, e.g.
 *                            https://keycloak.falabella.internal/realms/fdh
 *   NEXT_PUBLIC_SITE_URL   — public origin (used for the redirect_uri)
 */

import NextAuth from "next-auth";
import Keycloak from "next-auth/providers/keycloak";

export const { auth, handlers, signIn, signOut } = NextAuth({
  trustHost: true,
  session: { strategy: "jwt" },
  providers: [
    Keycloak({
      clientId: process.env.AUTH_KEYCLOAK_ID ?? "fdh-portal",
      clientSecret: process.env.AUTH_KEYCLOAK_SECRET ?? "dev-secret",
      issuer: process.env.AUTH_KEYCLOAK_ISSUER,
      // PKCE is enforced by default; we keep it explicit for clarity.
      checks: ["pkce", "state"],
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
