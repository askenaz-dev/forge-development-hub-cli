import Link from "next/link";
import { getTranslations } from "next-intl/server";
import { listSkills } from "@/lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { SkillsSearch } from "@/components/skills-search";

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
            {page.items.map((s) => (
              <Link
                key={`${s.namespace}/${s.name}`}
                href={`/skills/${s.namespace}/${s.name}`}
                className="block group"
              >
                <Card className="h-full transition-colors group-hover:border-primary">
                  <CardHeader>
                    <p className="font-mono text-xs text-muted-foreground">{s.namespace}</p>
                    <CardTitle className="text-base">{s.name}</CardTitle>
                  </CardHeader>
                  <CardContent>
                    <CardDescription className="line-clamp-3">{s.description}</CardDescription>
                    <div className="mt-3 flex items-center justify-between text-xs">
                      <span className="font-mono text-muted-foreground">v{s.latest_version}</span>
                      <ScanBadge status={s.scan_status} />
                    </div>
                  </CardContent>
                </Card>
              </Link>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function ScanBadge({ status }: { status: string }) {
  const color =
    status === "pass"
      ? "bg-green-500/10 text-green-700 dark:text-green-300"
      : status === "warn"
      ? "bg-yellow-500/10 text-yellow-700 dark:text-yellow-300"
      : status === "fail"
      ? "bg-destructive/10 text-destructive"
      : "bg-muted text-muted-foreground";
  return (
    <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${color}`}>
      {status}
    </span>
  );
}
