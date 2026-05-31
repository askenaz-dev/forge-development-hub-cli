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
              href="https://agentskills.io"
              target="_blank"
              rel="noreferrer noopener"
              className="hover:underline"
            >
              agentskills.io
            </Link>
          </nav>
        </div>
      </div>
    </footer>
  );
}
