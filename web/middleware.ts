import createMiddleware from "next-intl/middleware";
import { defaultLocale, locales } from "./i18n";

/**
 * Locale-aware routing middleware.
 *
 * Pages live under `app/[locale]/...`. This middleware rewrites incoming
 * URLs to the appropriate locale segment based on:
 *   - explicit URL prefix (`/en/skills` always wins)
 *   - persisted cookie set by the locale switcher
 *   - browser `Accept-Language`
 *   - the default locale as last resort
 *
 * `localePrefix: "as-needed"` means the default locale (`es`) serves
 * without a URL prefix; other locales serve under `/<locale>/...`.
 *
 * The matcher excludes API routes, Next internals, and any path with a
 * file extension (so static assets pass through untouched).
 */
export default createMiddleware({
  locales: [...locales],
  defaultLocale,
  localePrefix: "as-needed",
});

export const config = {
  matcher: ["/((?!api|_next|_vercel|.*\\..*).*)"],
};
