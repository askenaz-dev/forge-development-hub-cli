import createNextIntlPlugin from "next-intl/plugin";

// next-intl reads request locale from middleware; the plugin wires the
// i18n config file path.
const withNextIntl = createNextIntlPlugin("./i18n.ts");

/** @type {import("next").NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  poweredByHeader: false,
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
