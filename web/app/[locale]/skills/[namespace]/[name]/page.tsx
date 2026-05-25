import Link from "next/link";
import { notFound } from "next/navigation";
import { getTranslations } from "next-intl/server";
import { getSkill, getSkillMarkdown, ApiError, type SkillManifest } from "@/lib/api";
import { CopyCommand } from "@/components/copy-command";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { MarkdownView } from "@/components/markdown-view";

/**
 * /skills/[namespace]/[name] — skill detail.
 *
 * Server-rendered: fetches the manifest + raw SKILL.md from the portal
 * API, displays the install command in a copy block at the top of the
 * page, renders the markdown body, and surfaces version history +
 * per-agent install variants.
 */
export default async function SkillDetailPage({
  params,
}: {
  params: Promise<{ namespace: string; name: string }>;
}) {
  const { namespace, name } = await params;
  const t = await getTranslations("skillDetail");
  const tCommon = await getTranslations("common");

  let manifest: SkillManifest | null = null;
  try {
    manifest = await getSkill(namespace, name, { revalidate: 30 });
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) notFound();
    // Non-404 failures (network down, 5xx) — render an inline
    // fallback so the route degrades within the layout's <main>
    // instead of crashing the error boundary. The /skills index uses
    // the same graceful-fallback strategy.
  }

  if (!manifest) {
    return (
      <article className="container py-10">
        <h1 className="text-3xl font-bold tracking-tight">{name}</h1>
        <p className="mt-4 text-muted-foreground">{tCommon("error")}</p>
      </article>
    );
  }

  const latest = manifest.versions.find((v) => v.version === manifest.latest) ?? manifest.versions[0];
  let markdown = "";
  if (latest) {
    try {
      markdown = await getSkillMarkdown(namespace, name, latest.version, { revalidate: 60 });
    } catch {
      markdown = "_SKILL.md unavailable — try refreshing._";
    }
  }

  const installCommand = `fdh install ${namespace}/${name}`;
  const perAgent = [
    { id: "claude-code", label: "Claude Code", cmd: `fdh install ${namespace}/${name} --agent claude-code` },
    { id: "copilot", label: "GitHub Copilot", cmd: `fdh install ${namespace}/${name} --agent copilot` },
    { id: "codex", label: "OpenAI Codex", cmd: `fdh install ${namespace}/${name} --agent codex` },
    { id: "opencode", label: "OpenCode", cmd: `fdh install ${namespace}/${name} --agent opencode` },
  ];

  return (
    <article className="container py-10">
      <header className="space-y-3">
        <p className="font-mono text-sm text-muted-foreground">
          <Link href="/skills" className="hover:underline">
            ← {namespace}
          </Link>
        </p>
        <h1 className="text-3xl font-bold tracking-tight">{name}</h1>
        <p className="max-w-2xl text-muted-foreground">{manifest.description}</p>
        <dl className="flex flex-wrap gap-x-6 gap-y-2 text-xs text-muted-foreground">
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
        <div className="mt-2">
          <CopyCommand command={installCommand} />
        </div>
        <details className="mt-3 rounded-md border p-3 text-sm">
          <summary className="cursor-pointer font-medium">{t("perAgent")}</summary>
          <div className="mt-3 space-y-2">
            {perAgent.map((a) => (
              <div key={a.id}>
                <p className="text-xs font-medium text-muted-foreground">{a.label}</p>
                <CopyCommand command={a.cmd} />
              </div>
            ))}
          </div>
        </details>
      </section>

      <section className="mt-10">
        <MarkdownView markdown={markdown} />
      </section>

      <section className="mt-10">
        <Tabs defaultValue="versions">
          <TabsList>
            <TabsTrigger value="versions">{t("versionHistory")}</TabsTrigger>
          </TabsList>
          <TabsContent value="versions">
            <Card>
              <CardHeader>
                <CardTitle className="text-base">Versions</CardTitle>
              </CardHeader>
              <CardContent className="space-y-2">
                {manifest.versions.map((v) => (
                  <div
                    key={v.version}
                    className="flex flex-wrap items-baseline justify-between gap-2 border-b py-2 last:border-0"
                  >
                    <div>
                      <Link
                        href={`/skills/${namespace}/${name}/versions/${v.version}`}
                        className="font-mono text-sm hover:underline"
                      >
                        v{v.version}
                      </Link>
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
          </TabsContent>
        </Tabs>
      </section>
    </article>
  );
}
