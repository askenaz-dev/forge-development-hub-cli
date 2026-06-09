import createNextIntlPlugin from "next-intl/plugin";

// next-intl reads request locale from middleware; the plugin wires the
// i18n config file path.
const withNextIntl = createNextIntlPlugin("./i18n.ts");

// ── Avatar provider → CSP img-src (portal-admin-surface D5) ─────────────────
// The /profile avatar is Gravatar by default; PORTAL_AVATAR_PROVIDER=local
// switches to a fully-offline initials SVG (data: URI). We add a CSP ONLY on
// the /profile route (see headers() below) and allow the Gravatar origin in
// img-src ONLY when the gravatar provider is active. This is additive and
// scoped: the rest of the portal — which ships no CSP today — is untouched, so
// login, the header, and the catalog cannot be affected by this change.
const AVATAR_PROVIDER =
  process.env.PORTAL_AVATAR_PROVIDER?.toLowerCase() === "local"
    ? "local"
    : "gravatar";

// `data:` covers the local provider's inline SVG and any data-URI icons; the
// Gravatar origin is appended only when that provider is selected. `'self'`
// keeps same-origin images (e.g. /icon.svg) working.
const PROFILE_IMG_SRC =
  AVATAR_PROVIDER === "gravatar"
    ? "'self' data: https://www.gravatar.com"
    : "'self' data:";

// A deliberately permissive CSP that constrains only what we need here
// (img-src for the avatar) while leaving script/style/connect/font as
// permissive as the un-CSP'd baseline so Next.js's inline runtime, the theme
// provider, fonts, and same-origin API calls keep working on /profile.
const PROFILE_CSP = [
  "default-src 'self'",
  `img-src ${PROFILE_IMG_SRC}`,
  "script-src 'self' 'unsafe-inline' 'unsafe-eval'",
  "style-src 'self' 'unsafe-inline'",
  "font-src 'self' data:",
  "connect-src 'self'",
  "frame-ancestors 'self'",
  "base-uri 'self'",
].join("; ");

/** @type {import("next").NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  poweredByHeader: false,
  // Per-route security headers. Scoped to /profile (and its locale-prefixed
  // form) so the only page that renders a Gravatar gets the CSP that permits
  // it — additively, without imposing a brand-new CSP on the working portal.
  async headers() {
    return [
      {
        // `es` (default locale) serves /profile unprefixed; `en` serves
        // /en/profile. Cover both so the avatar CSP applies regardless of
        // locale. No other route receives this header.
        source: "/:locale(en)?/profile",
        headers: [{ key: "Content-Security-Policy", value: PROFILE_CSP }],
      },
    ];
  },
  // Emit a self-contained server bundle Docker can copy without bringing
  // node_modules along. Solves the pnpm-strict-hoist + styled-jsx problem
  // for containerized deploys.
  output: "standalone",
  experimental: {
    // typedRoutes interacts poorly with the `[locale]` dynamic segment:
    // every Link href in the codebase targets paths like `/skills`,
    // but typedRoutes resolves them through the segment as
    // `/[locale]/skills`, demanding `as Route` casts everywhere.
    // Disabled for now — re-enable in a follow-up that either casts
    // hrefs or adopts next-intl's typed Link wrapper.
    typedRoutes: false,
    // Enables instrumentation.ts `register()` (stable in Next 15; behind this
    // flag on 14.2). We use it to install the no-compression fetch shim at
    // process startup so background ISR revalidations are covered too.
    instrumentationHook: true,
  },
  eslint: {
    // ESLint runs as a separate `pnpm lint` step. Builds skip it to keep
    // CI fast and to decouple linting policy from build success.
    ignoreDuringBuilds: true,
  },
  // FDH_API_BASE_URL is intentionally NOT in `env` here. Listing it would
  // inline its build-time value into the bundle, making it impossible to
  // override at runtime. `lib/api.ts` reads `process.env.FDH_API_BASE_URL`
  // server-side on each request instead.
};

export default withNextIntl(nextConfig);
