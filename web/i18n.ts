import { getRequestConfig } from "next-intl/server";

/**
 * Supported locales. `es` is the default, matching forge's primary
 * language; `en` covers the broader engineering org and contractors.
 *
 * Pages live under `app/[locale]/`. The next-intl middleware rewrites
 * URLs to populate the locale segment; this `getRequestConfig` resolver
 * reads the resulting locale from `requestLocale` and loads the matching
 * message bundle.
 *
 * Adding a new locale: create messages/<code>.json with key parity to
 * messages/es.json (CI enforces parity), append the code to `locales`
 * below, and the routing + locale switcher pick it up automatically.
 */
export const locales = ["es", "en"] as const;
export const defaultLocale = "es" as const;

export type Locale = (typeof locales)[number];

export default getRequestConfig(async ({ requestLocale }) => {
  let locale = await requestLocale;
  if (!locale || !locales.includes(locale as Locale)) {
    locale = defaultLocale;
  }
  const messages = (await import(`./messages/${locale}.json`)).default;
  return { locale, messages };
});
