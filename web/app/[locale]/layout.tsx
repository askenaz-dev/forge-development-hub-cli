import "../globals.css";
import type { Metadata } from "next";
import { GeistSans } from "geist/font/sans";
import { GeistMono } from "geist/font/mono";
import { setRequestLocale, getMessages } from "next-intl/server";
import { NextIntlClientProvider } from "next-intl";
import { notFound } from "next/navigation";
import { ThemeProvider } from "@/components/theme-provider";
import { SiteNav } from "@/components/site-nav";
import { SiteFooter } from "@/components/site-footer";
import { locales, type Locale } from "@/i18n";

/**
 * Root + locale layout for the FDH portal.
 *
 * This file is the ONLY layout in the app. It owns:
 *   - <html>, <body>, global fonts, globals.css (Next.js root-layout
 *     responsibilities)
 *   - the locale-aware `lang` attribute (a11y requirement)
 *   - the intl provider + theme provider + nav/footer chrome
 *
 * Why no separate `app/layout.tsx`: pages live under `app/[locale]/`.
 * If we placed <html> in the root layout, it couldn't read the locale
 * (params arrive one level deeper). Owning <html> here keeps `lang`
 * accurate without resorting to client-side mutation.
 */

export const metadata: Metadata = {
  title: {
    default: "Forge Development Hub",
    template: "%s · Forge Development Hub",
  },
  description:
    "Discover, install, and govern skills, rules, agents, and hooks across Claude Code, GitHub Copilot, OpenAI Codex, and OpenCode.",
  metadataBase: new URL(
    process.env.NEXT_PUBLIC_SITE_URL ?? "http://localhost:3000"
  ),
  icons: {
    icon: "/icon.svg",
    shortcut: "/icon.svg",
    apple: "/icon.svg",
  },
};

export default async function LocaleLayout({
  children,
  params,
}: {
  children: React.ReactNode;
  params: Promise<{ locale: string }>;
}) {
  const { locale } = await params;
  if (!locales.includes(locale as Locale)) {
    notFound();
  }
  setRequestLocale(locale);
  const messages = await getMessages();

  return (
    <html
      lang={locale}
      suppressHydrationWarning
      className={`${GeistSans.variable} ${GeistMono.variable}`}
    >
      <body className="min-h-screen flex flex-col font-sans antialiased">
        <a href="#main" className="skip-link">
          Skip to main content
        </a>
        <ThemeProvider>
          <NextIntlClientProvider locale={locale} messages={messages}>
            <SiteNav />
            <main id="main" className="flex-1">
              {children}
            </main>
            <SiteFooter />
          </NextIntlClientProvider>
        </ThemeProvider>
      </body>
    </html>
  );
}

export function generateStaticParams() {
  return locales.map((locale) => ({ locale }));
}
