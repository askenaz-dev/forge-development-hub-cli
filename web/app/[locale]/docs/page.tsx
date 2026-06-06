import Link from "next/link";
import { getTranslations, setRequestLocale } from "next-intl/server";
import {
  BookOpen,
  PenLine,
  Wrench,
  Scale,
  ExternalLink,
  Sparkles,
  ScrollText,
  Bot,
  Webhook,
  ShieldCheck,
  Download,
  Globe,
} from "lucide-react";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { CopyCommand } from "@/components/copy-command";

/**
 * /docs — the in-site guides hub.
 *
 * The canonical, copy-pasteable references live in the repo (docs/*.md +
 * CONTRIBUTING.md) and are kept in sync with CI. This page is the map: it
 * surfaces the "create a component + add it to the hub" flow on the portal
 * itself (so it's discoverable without leaving the site) and links out to the
 * full guides on GitHub. Server-rendered and fully localized.
 */

const HUB = "https://github.com/askenaz-dev/forge-development-hub";
const CLI = "https://github.com/askenaz-dev/forge-development-hub-cli";

export default async function DocsPage({
  params,
}: {
  params: Promise<{ locale: string }>;
}) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("docs");

  const primitives = [
    { icon: Sparkles, title: t("primSkill"), body: t("primSkillBody") },
    { icon: ScrollText, title: t("primRule"), body: t("primRuleBody") },
    { icon: Bot, title: t("primAgent"), body: t("primAgentBody") },
    { icon: Webhook, title: t("primHook"), body: t("primHookBody") },
  ];

  const cliSteps = [
    {
      title: t("cliStep1Title"),
      body: t("cliStep1Body"),
      command: "fdh skill new card-grid",
    },
    {
      title: t("cliStep2Title"),
      body: t("cliStep2Body"),
      command: "fdh skill sync card-grid",
    },
    {
      title: t("cliStep3Title"),
      body: t("cliStep3Body"),
      command: "fdh skill share card-grid --repo <path-to-hub-checkout>",
    },
  ];

  const noCliSteps = [t("noCliStep1"), t("noCliStep2"), t("noCliStep3")];

  const gates = [
    { title: t("gate1Title"), body: t("gate1Body") },
    { title: t("gate2Title"), body: t("gate2Body") },
    { title: t("gate3Title"), body: t("gate3Body") },
  ];

  const references = [
    {
      icon: BookOpen,
      title: t("refHubGuide"),
      desc: t("refHubGuideDesc"),
      href: `${HUB}/blob/main/docs/hub-guide.md`,
    },
    {
      icon: PenLine,
      title: t("refAuthoringGuide"),
      desc: t("refAuthoringGuideDesc"),
      href: `${HUB}/blob/main/docs/authoring-guide.md`,
    },
    {
      icon: Wrench,
      title: t("refRunbook"),
      desc: t("refRunbookDesc"),
      href: `${HUB}/blob/main/docs/maintainer-runbook.md`,
    },
    {
      icon: Scale,
      title: t("refContributing"),
      desc: t("refContributingDesc"),
      href: `${HUB}/blob/main/CONTRIBUTING.md`,
    },
  ];

  return (
    <div className="container py-12">
      <header className="mx-auto max-w-3xl text-center">
        <h1 className="text-3xl font-bold tracking-tight">{t("title")}</h1>
        <p className="mt-3 text-muted-foreground">{t("intro")}</p>
      </header>

      {/* The four primitives */}
      <section className="mx-auto mt-14 max-w-5xl">
        <h2 className="text-xl font-semibold tracking-tight">
          {t("primitivesHeading")}
        </h2>
        <p className="mt-2 text-sm text-muted-foreground">
          {t("primitivesLead")}
        </p>
        <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {primitives.map((p) => {
            const Icon = p.icon;
            return (
              <Card key={p.title}>
                <CardHeader>
                  <Icon className="h-5 w-5 text-ember" aria-hidden="true" />
                  <CardTitle className="text-base">{p.title}</CardTitle>
                </CardHeader>
                <CardContent>
                  <p className="text-sm text-muted-foreground">{p.body}</p>
                </CardContent>
              </Card>
            );
          })}
        </div>
      </section>

      {/* Install & use */}
      <section className="mx-auto mt-16 max-w-5xl">
        <h2 className="text-xl font-semibold tracking-tight">
          {t("installHeading")}
        </h2>
        <p className="mt-2 text-sm text-muted-foreground">{t("installLead")}</p>
        <div className="mt-6 grid gap-4 md:grid-cols-2">
          {/* From the Forge hub */}
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                <Download className="h-5 w-5 text-ember" aria-hidden="true" />
                {t("installHubTitle")}
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <p className="text-sm text-muted-foreground">
                {t("installHubBody")}
              </p>
              <CopyCommand command="fdh init" />
              <Link
                href="/install"
                className="inline-block text-sm font-medium text-ember hover:underline"
              >
                {t("installHubCta")} →
              </Link>
            </CardContent>
          </Card>

          {/* From an external source */}
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                <Globe className="h-5 w-5 text-ember" aria-hidden="true" />
                {t("installExtTitle")}
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <p className="text-sm text-muted-foreground">
                {t("installExtBody")}
              </p>
              <CopyCommand command="fdh config set registry.url https://github.com/your-org/your-hub.git" />
              <p className="text-xs text-muted-foreground">
                {t("installExtNote")}
              </p>
              <Link
                href={`${CLI}/blob/main/docs/quickstart.md`}
                target="_blank"
                rel="noreferrer noopener"
                className="inline-flex items-center gap-1.5 text-sm font-medium text-ember hover:underline"
              >
                {t("installQuickstartCta")}
                <ExternalLink className="h-4 w-4" aria-hidden="true" />
              </Link>
            </CardContent>
          </Card>
        </div>
      </section>

      {/* Create a new component */}
      <section className="mx-auto mt-16 max-w-5xl">
        <h2 className="text-xl font-semibold tracking-tight">
          {t("createHeading")}
        </h2>
        <p className="mt-2 text-sm text-muted-foreground">{t("createLead")}</p>

        {/* Path A — CLI */}
        <div className="mt-8">
          <h3 className="text-lg font-medium">{t("cliHeading")}</h3>
          <p className="mt-1 text-sm text-muted-foreground">{t("cliLead")}</p>
          <ol className="mt-5 space-y-4">
            {cliSteps.map((s, i) => (
              <li key={s.title}>
                <Card>
                  <CardHeader>
                    <CardTitle className="flex items-center gap-2 text-base">
                      <span className="inline-flex h-6 w-6 items-center justify-center rounded-full bg-ember/15 text-sm font-semibold text-ember">
                        {i + 1}
                      </span>
                      {s.title}
                    </CardTitle>
                  </CardHeader>
                  <CardContent className="space-y-3">
                    <p className="text-sm text-muted-foreground">{s.body}</p>
                    <CopyCommand command={s.command} />
                  </CardContent>
                </Card>
              </li>
            ))}
          </ol>
        </div>

        {/* Path B — no CLI */}
        <div className="mt-10">
          <h3 className="text-lg font-medium">{t("noCliHeading")}</h3>
          <p className="mt-1 text-sm text-muted-foreground">{t("noCliLead")}</p>
          <Card className="mt-5">
            <CardContent className="pt-6">
              <ol className="space-y-3">
                {noCliSteps.map((step, i) => (
                  <li key={i} className="flex gap-3 text-sm">
                    <span className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-muted text-sm font-semibold">
                      {i + 1}
                    </span>
                    <span className="pt-0.5 text-muted-foreground">{step}</span>
                  </li>
                ))}
              </ol>
            </CardContent>
          </Card>
        </div>

        {/* Validate locally */}
        <div className="mt-10">
          <h3 className="text-lg font-medium">{t("validateHeading")}</h3>
          <p className="mt-1 text-sm text-muted-foreground">
            {t("validateLead")}
          </p>
          <div className="mt-4 max-w-2xl">
            <CopyCommand command="python tools/validate-registry.py" />
          </div>
        </div>
      </section>

      {/* Three gates */}
      <section className="mx-auto mt-16 max-w-5xl">
        <h2 className="flex items-center gap-2 text-xl font-semibold tracking-tight">
          <ShieldCheck className="h-5 w-5 text-ember" aria-hidden="true" />
          {t("gatesHeading")}
        </h2>
        <p className="mt-2 text-sm text-muted-foreground">{t("gatesLead")}</p>
        <div className="mt-6 grid gap-4 md:grid-cols-3">
          {gates.map((g) => (
            <Card key={g.title}>
              <CardHeader>
                <CardTitle className="text-base">{g.title}</CardTitle>
              </CardHeader>
              <CardContent>
                <p className="text-sm text-muted-foreground">{g.body}</p>
              </CardContent>
            </Card>
          ))}
        </div>
      </section>

      {/* Full references */}
      <section className="mx-auto mt-16 max-w-5xl">
        <h2 className="text-xl font-semibold tracking-tight">
          {t("referencesHeading")}
        </h2>
        <p className="mt-2 text-sm text-muted-foreground">
          {t("referencesLead")}
        </p>
        <div className="mt-6 grid gap-4 sm:grid-cols-2">
          {references.map((r) => {
            const Icon = r.icon;
            return (
              <Link
                key={r.title}
                href={r.href}
                target="_blank"
                rel="noreferrer noopener"
                className="group rounded-lg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <Card className="h-full transition-colors group-hover:border-ember/50">
                  <CardHeader>
                    <CardTitle className="flex items-center gap-2 text-base">
                      <Icon className="h-5 w-5 text-ember" aria-hidden="true" />
                      {r.title}
                      <ExternalLink
                        className="ml-auto h-4 w-4 text-muted-foreground transition-colors group-hover:text-foreground"
                        aria-hidden="true"
                      />
                    </CardTitle>
                    <CardDescription>{r.desc}</CardDescription>
                  </CardHeader>
                  <CardContent>
                    <span className="text-sm font-medium text-ember">
                      {t("openOnGithub")} →
                    </span>
                  </CardContent>
                </Card>
              </Link>
            );
          })}
        </div>
      </section>
    </div>
  );
}
