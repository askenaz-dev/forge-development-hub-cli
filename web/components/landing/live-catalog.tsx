import Link from "next/link";
import { ArrowRight } from "lucide-react";
import { getTranslations } from "next-intl/server";
import { Button } from "@/components/ui/button";
import { Reveal } from "@/components/motion/reveal";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import type { LandingData } from "@/lib/landing-data";

const KIND_PATH: Record<string, string> = {
  skill: "/skills",
  rule: "/rules",
  agent: "/agents",
  hook: "/hooks",
};

/**
 * LiveCatalog — a sample of real, published components from the catalog API
 * (ISR data passed in from the page). Each card is a staggered <Reveal> and
 * links to the real detail page. When the API is unavailable the sample is
 * empty and we show a friendly prompt + a CTA into the catalog instead of an
 * error (portal-web graceful-degradation requirement).
 */
export async function LiveCatalog({ data }: { data: LandingData }) {
  const t = await getTranslations("landing");

  return (
    <section className="border-y bg-muted/30 py-20">
      <div className="container">
        <div className="mx-auto max-w-2xl text-center">
          <h2 className="text-3xl font-bold tracking-tight">{t("catalogHeading")}</h2>
          <p className="mt-3 text-muted-foreground">{t("catalogSub")}</p>
        </div>

        {data.sample.length > 0 ? (
          <div className="mx-auto mt-12 grid max-w-5xl gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {data.sample.map((c, i) => (
              <Reveal key={`${c.kind}/${c.namespace}/${c.name}`} delayMs={i * 70}>
                <Link
                  href={`${KIND_PATH[c.kind] ?? "/skills"}/${c.namespace}/${c.name}`}
                  className="group block h-full"
                >
                  <Card className="forge-glow-hover h-full">
                    <CardHeader>
                      <div className="flex items-center justify-between">
                        <span className="inline-flex items-center rounded-full border border-ember/30 bg-ember/10 px-2 py-0.5 text-xs font-medium capitalize">
                          {c.kind}
                        </span>
                        <span className="font-mono text-xs text-muted-foreground">
                          v{c.latest_version}
                        </span>
                      </div>
                      <p className="mt-2 font-mono text-xs text-muted-foreground">
                        {c.namespace}
                      </p>
                      <h3 className="text-base font-semibold tracking-tight">{c.name}</h3>
                    </CardHeader>
                    <CardContent>
                      <p className="line-clamp-3 text-sm text-muted-foreground">
                        {c.description}
                      </p>
                    </CardContent>
                  </Card>
                </Link>
              </Reveal>
            ))}
          </div>
        ) : (
          <p className="mt-10 text-center text-muted-foreground">{t("catalogEmpty")}</p>
        )}

        <div className="mt-10 text-center">
          <Button asChild variant="outline" className="border-ember/40 hover:shadow-glow">
            <Link href="/skills">
              {t("catalogCta")} <ArrowRight className="h-4 w-4" />
            </Link>
          </Button>
        </div>
      </div>
    </section>
  );
}
