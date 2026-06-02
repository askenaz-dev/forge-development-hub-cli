import Link from "next/link";
import { getTranslations } from "next-intl/server";
import { listComponents, type Kind } from "@/lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { CatalogSearch } from "@/components/catalog-search";
import { Reveal } from "@/components/motion/reveal";
import { ScanStatusBadge } from "@/components/scan-status-badge";

/**
 * ComponentCatalog is the shared, server-rendered browse grid for one kind
 * (skill | rule | agent | hook). The kind tabs in the nav point at the
 * per-kind routes that render this component; the search island updates the
 * URL so first paint always contains real, server-rendered content.
 */
export async function ComponentCatalog({
  kind,
  basePath,
  q,
}: {
  kind: Kind;
  basePath: string;
  q: string;
}) {
  const t = await getTranslations("catalog");
  const tKinds = await getTranslations("kinds");
  const kindLabel = tKinds(kind);

  const page = await listComponents(
    { kind, q, limit: 50 },
    { revalidate: 30 }
  ).catch(() => ({ items: [], next_cursor: null }));

  return (
    <div className="container py-12">
      <header className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <h1 className="text-3xl font-bold tracking-tight">{t("title", { kind: kindLabel })}</h1>
        <CatalogSearch
          basePath={basePath}
          initialQuery={q}
          placeholder={t("searchPlaceholder", { kind: kindLabel })}
          ariaLabel={t("searchPlaceholder", { kind: kindLabel })}
        />
      </header>

      <div className="mt-8">
        {page.items.length === 0 ? (
          <p className="text-muted-foreground">{t("empty", { kind: kindLabel })}</p>
        ) : (
          <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            {page.items.map((c, i) => (
              <Reveal key={`${c.namespace}/${c.name}`} delayMs={Math.min(i, 8) * 60}>
                <Link
                  href={`${basePath}/${c.namespace}/${c.name}`}
                  className="block group h-full"
                >
                  <Card className="forge-glow-hover h-full">
                    <CardHeader>
                      <p className="font-mono text-xs text-muted-foreground">{c.namespace}</p>
                      <CardTitle className="text-base">{c.name}</CardTitle>
                    </CardHeader>
                    <CardContent>
                      <CardDescription className="line-clamp-3">{c.description}</CardDescription>
                      <div className="mt-3 flex items-center justify-between text-xs">
                        <span className="font-mono text-muted-foreground">v{c.latest_version}</span>
                        <ScanStatusBadge status={c.scan_status} />
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
