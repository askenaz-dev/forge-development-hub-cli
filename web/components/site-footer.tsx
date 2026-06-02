import Link from "next/link";
import { Flame } from "lucide-react";
import { getTranslations } from "next-intl/server";

/**
 * SiteFooter — global footer. Responsive: a single stacked column on mobile,
 * a multi-column layout on `sm+`. Enriched with product/resource links to
 * match the narrative landing, plus a build-info label so users can report
 * against a specific deploy.
 */
export async function SiteFooter() {
  const t = await getTranslations("footer");
  const tNav = await getTranslations("nav");
  const buildVersion = process.env.NEXT_PUBLIC_FDH_BUILD ?? "dev";

  const columns: { heading: string; links: { href: string; label: string }[] }[] = [
    {
      heading: t("catalog"),
      links: [
        { href: "/skills", label: tNav("skills") },
        { href: "/rules", label: tNav("rules") },
        { href: "/agents", label: tNav("agents") },
        { href: "/hooks", label: tNav("hooks") },
      ],
    },
    {
      heading: t("getStartedHeading"),
      links: [
        { href: "/install", label: tNav("install") },
        { href: "/onboarding", label: tNav("getStarted") },
        { href: "/auth/signin", label: tNav("signIn") },
      ],
    },
  ];

  return (
    <footer className="border-t bg-background">
      <div className="container py-10">
        <div className="grid gap-8 sm:grid-cols-2 md:grid-cols-4">
          {/* Brand blurb */}
          <div className="space-y-3 sm:col-span-2 md:col-span-2">
            <div className="flex items-center gap-2 font-semibold">
              <Flame className="h-5 w-5 text-ember" />
              <span>Forge Development Hub</span>
            </div>
            <p className="max-w-xs text-sm text-muted-foreground">
              {t("blurb")}
            </p>
          </div>

          {columns.map((col) => (
            <nav key={col.heading} className="space-y-3 text-sm">
              <h3 className="font-medium text-foreground">{col.heading}</h3>
              <ul className="space-y-2">
                {col.links.map((l) => (
                  <li key={l.href}>
                    <Link
                      href={l.href}
                      className="text-muted-foreground transition-colors hover:text-foreground"
                    >
                      {l.label}
                    </Link>
                  </li>
                ))}
              </ul>
            </nav>
          ))}
        </div>

        <div className="mt-10 flex flex-col items-center justify-between gap-3 border-t pt-6 text-xs text-muted-foreground sm:flex-row">
          <p>
            Forge Development Hub · build{" "}
            <code className="font-mono">{buildVersion}</code>
          </p>
          <nav className="flex flex-wrap items-center justify-center gap-x-4 gap-y-2">
            <Link href="/docs" className="hover:underline">
              {t("docs")}
            </Link>
            <Link href="/accessibility" className="hover:underline">
              {t("accessibility")}
            </Link>
            <Link
              href="https://github.com/askenaz-dev/forge-development-hub"
              target="_blank"
              rel="noreferrer noopener"
              aria-label="Forge Development Hub on GitHub"
              className="inline-flex items-center gap-1.5 hover:underline"
            >
              <svg
                viewBox="0 0 16 16"
                className="h-4 w-4"
                fill="currentColor"
                aria-hidden="true"
              >
                <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z" />
              </svg>
              GitHub
            </Link>
          </nav>
        </div>
      </div>
    </footer>
  );
}
