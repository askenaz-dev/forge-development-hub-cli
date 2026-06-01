import Link from "next/link";
import { ArrowRight } from "lucide-react";
import { getTranslations } from "next-intl/server";
import { Button } from "@/components/ui/button";
import { CountUp } from "@/components/motion/count-up";
import type { LandingData } from "@/lib/landing-data";

/**
 * Hero — the Ember Forge above-the-fold section.
 *
 * Server-rendered (the tagline, subhead, and CTAs are in the first paint, per
 * portal-web). The molten gradient + sparks are decorative CSS layers
 * (aria-hidden, paused under reduced motion). The stat chips are <CountUp>
 * client islands fed by live ISR data; when the API is down (`data.live` is
 * false) the numeric chips are omitted rather than showing zeros.
 */
export async function Hero({ data }: { data: LandingData }) {
  const t = await getTranslations("landing");

  return (
    <section className="relative isolate overflow-hidden border-b">
      {/* Decorative molten background + sparks (non-interactive). */}
      <div
        aria-hidden="true"
        className="forge-molten-bg pointer-events-none absolute inset-0 -z-10"
      />
      <div
        aria-hidden="true"
        className="forge-sparks pointer-events-none absolute inset-0 -z-10 opacity-70"
      />

      <div className="container py-20 md:py-28">
        <div className="mx-auto max-w-3xl text-center">
          <span className="inline-flex items-center rounded-full border border-ember/40 bg-ember/10 px-3 py-1 text-xs font-medium text-foreground">
            {t("eyebrow")}
          </span>

          <h1 className="mt-6 text-balance text-4xl font-bold tracking-tight md:text-6xl">
            <span className="text-molten">{t("tagline")}</span>
          </h1>

          <p className="mx-auto mt-6 max-w-2xl text-balance text-lg text-muted-foreground">
            {t("subhead")}
          </p>

          <div className="mt-10 flex flex-col items-center justify-center gap-3 sm:flex-row">
            <Button asChild size="lg">
              <Link href="/install">
                {t("ctaInstall")} <ArrowRight className="h-4 w-4" />
              </Link>
            </Button>
            <Button
              asChild
              size="lg"
              variant="outline"
              className="border-ember/40 hover:shadow-glow"
            >
              <Link href="/skills">{t("ctaBrowse")}</Link>
            </Button>
          </div>

          {/* Catalog composition — one chip per kind, with live counts.
              When the API is down we still show the four kind labels (no
              fabricated numbers), so the four-primitive story always reads. */}
          <dl className="mx-auto mt-12 flex max-w-xl flex-wrap items-center justify-center gap-x-10 gap-y-4">
            <Stat live={data.live} value={data.byKind.skill} label={t("statSkills")} />
            <Stat live={data.live} value={data.byKind.rule} label={t("statRules")} />
            <Stat live={data.live} value={data.byKind.agent} label={t("statAgents")} />
            <Stat live={data.live} value={data.byKind.hook} label={t("statHooks")} />
          </dl>
        </div>
      </div>
    </section>
  );
}

function Stat({
  value,
  label,
  live,
}: {
  value: number;
  label: string;
  live: boolean;
}) {
  return (
    <div className="flex flex-col items-center">
      {live && (
        <dd className="text-3xl font-bold tracking-tight text-foreground md:text-4xl">
          <CountUp to={value} />
        </dd>
      )}
      <dt className={live ? "mt-1 text-sm text-muted-foreground" : "text-sm text-muted-foreground"}>
        {label}
      </dt>
    </div>
  );
}
