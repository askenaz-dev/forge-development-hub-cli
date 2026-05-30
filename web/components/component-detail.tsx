import Link from "next/link";
import { notFound } from "next/navigation";
import { getTranslations } from "next-intl/server";
import {
  getComponent,
  getComponentDocument,
  ApiError,
  type ComponentManifest,
  type Kind,
} from "@/lib/api";
import { CopyCommand } from "@/components/copy-command";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { MarkdownView } from "@/components/markdown-view";

/**
 * ComponentDetail is the shared, server-rendered detail view for one
 * component of any kind. It renders the kind's entrypoint document, version
 * history, and a kind-correct install affordance:
 *   - skills get the real `fdh install <ns>/<name>` command;
 *   - other kinds get an honest note (the CLI installs them via `fdh init` /
 *     hub profiles, not a direct `fdh install <ref>`), so we never print a
 *     command the CLI does not accept.
 */
export async function ComponentDetail({
  kind,
  namespace,
  name,
  basePath,
}: {
  kind: Kind;
  namespace: string;
  name: string;
  basePath: string;
}) {
  const t = await getTranslations("componentDetail");
  const tKinds = await getTranslations("kinds");
  const tCommon = await getTranslations("common");
  const kindLabel = tKinds(kind);

  let manifest: ComponentManifest | null = null;
  try {
    manifest = await getComponent(kind, namespace, name, { revalidate: 30 });
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) notFound();
    // Non-404 (network/5xx): degrade inline instead of crashing.
  }

  if (!manifest) {
    return (
      <article className="container py-10">
        <h1 className="text-3xl font-bold tracking-tight">{name}</h1>
        <p className="mt-4 text-muted-foreground">{tCommon("error")}</p>
      </article>
    );
  }

  const latest =
    manifest.versions.find((v) => v.version === manifest.latest) ?? manifest.versions[0];
  let markdown = "";
  if (latest) {
    try {
      markdown = await getComponentDocument(kind, namespace, name, latest.version, {
        revalidate: 60,
      });
    } catch {
      markdown = "_Document unavailable — try refreshing._";
    }
  }

  return (
    <article className="container py-10">
      <header className="space-y-3">
        <p className="font-mono text-sm text-muted-foreground">
          <Link href={basePath} className="hover:underline">
            ← {kindLabel}
          </Link>
        </p>
        <h1 className="text-3xl font-bold tracking-tight">{name}</h1>
        <p className="max-w-2xl text-muted-foreground">{manifest.description}</p>
        <dl className="flex flex-wrap gap-x-6 gap-y-2 text-xs text-muted-foreground">
          <div>
            <dt className="inline font-semibold">{t("kind")}: </dt>
            <dd className="inline">{manifest.kind}</dd>
          </div>
          {manifest.owner_team && (
            <div>
              <dt className="inline font-semibold">{t("ownerTeam")}: </dt>
              <dd className="inline">{manifest.owner_team}</dd>
            </div>
          )}
          {latest && (
            <div>
              <dt className="inline font-semibold">{t("scanStatus")}: </dt>
              <dd className="inline">{latest.scan_status}</dd>
            </div>
          )}
          {manifest.tags && manifest.tags.length > 0 && (
            <div>
              <dt className="inline font-semibold">tags: </dt>
              <dd className="inline">{manifest.tags.join(", ")}</dd>
            </div>
          )}
        </dl>
      </header>

      <section className="mt-8">
        <h2 className="text-sm font-semibold uppercase tracking-wide text-muted-foreground">
          {t("installCommand")}
        </h2>
        {manifest.kind === "skill" ? (
          <div className="mt-2">
            <CopyCommand command={`fdh install ${namespace}/${name}`} />
          </div>
        ) : (
          <p className="mt-2 max-w-2xl rounded-md border bg-muted/30 p-3 text-sm text-muted-foreground">
            {t("installUnavailable", { kind: kindLabel })}
          </p>
        )}
      </section>

      <section className="mt-10">
        <MarkdownView markdown={markdown} />
      </section>

      <section className="mt-10">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">{t("versionHistory")}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            {manifest.versions.map((v) => (
              <div
                key={v.version}
                className="flex flex-wrap items-baseline justify-between gap-2 border-b py-2 last:border-0"
              >
                <div>
                  <span className="font-mono text-sm">v{v.version}</span>
                  <span className="ml-3 text-xs text-muted-foreground">
                    {new Date(v.published_at).toLocaleDateString()}
                  </span>
                </div>
                <span className="font-mono text-xs text-muted-foreground">
                  {v.content_hash.slice(0, 12)}
                </span>
              </div>
            ))}
          </CardContent>
        </Card>
      </section>
    </article>
  );
}
