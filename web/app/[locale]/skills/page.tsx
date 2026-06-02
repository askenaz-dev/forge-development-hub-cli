import Link from "next/link";
import { getTranslations } from "next-intl/server";
import { listSkills } from "@/lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { SkillsSearch } from "@/components/skills-search";
import { Reveal } from "@/components/motion/reveal";
import { ScanStatusBadge } from "@/components/scan-status-badge";

/**
 * /skills — server-rendered, SEO-friendly catalog.
 *
 * The search input is a tiny client island. The list itself renders
 * server-side, including when a `?q=` query parameter is present, so the
 * URL is shareable and first paint contains real content.
 */
export default async function SkillsPage({
  searchParams,
}: {
  searchParams: Promise<{ q?: string; namespace?: string; tag?: string }>;
}) {
  const t = await getTranslations("browse");
  const params = await searchParams;
  const q = params.q ?? "";

  const page = await listSkills(
    { q, namespace: params.namespace, tag: params.tag, limit: 50 },
    { revalidate: 30 }
  ).catch(() => ({ items: [], next_cursor: null }));

  return (
    <div className="container py-12">
      <header className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <h1 className="text-3xl font-bold tracking-tight">{t("title")}</h1>
        <SkillsSearch initialQuery={q} placeholder={t("searchPlaceholder")} />
      </header>

      <div className="mt-8">
        {page.items.length === 0 ? (
          <p className="text-muted-foreground">{t("empty")}</p>
        ) : (
          <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            {page.items.map((s, i) => (
              <Reveal key={`${s.namespace}/${s.name}`} delayMs={Math.min(i, 8) * 60}>
                <Link
                  href={`/skills/${s.namespace}/${s.name}`}
                  className="block group h-full"
                >
                  <Card className="forge-glow-hover h-full">
                    <CardHeader>
                      <p className="font-mono text-xs text-muted-foreground">{s.namespace}</p>
                      <CardTitle className="text-base">{s.name}</CardTitle>
                    </CardHeader>
                    <CardContent>
                      <CardDescription className="line-clamp-3">{s.description}</CardDescription>
                      <div className="mt-3 flex items-center justify-between text-xs">
                        <span className="font-mono text-muted-foreground">v{s.latest_version}</span>
                        <ScanStatusBadge status={s.scan_status} />
                      </div>
                    </CardContent>
                  </Card>
                </Link>
              </Reveal>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
