import Link from "next/link";
import { ArrowRight } from "lucide-react";
import { getTranslations } from "next-intl/server";
import { Button } from "@/components/ui/button";

/**
 * FinalCta — closing conversion band with the molten gradient motif.
 * Server-rendered.
 */
export async function FinalCta() {
  const t = await getTranslations("landing");
  return (
    <section className="relative isolate overflow-hidden border-t">
      <div
        aria-hidden="true"
        className="forge-molten-bg pointer-events-none absolute inset-0 -z-10 opacity-80"
      />
      <div className="container py-20 text-center">
        <h2 className="mx-auto max-w-2xl text-balance text-3xl font-bold tracking-tight md:text-4xl">
          {t("finalCtaHeading")}
        </h2>
        <p className="mx-auto mt-4 max-w-xl text-muted-foreground">
          {t("finalCtaBody")}
        </p>
        <div className="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
          <Button asChild size="lg">
            <Link href="/install">
              {t("ctaInstall")} <ArrowRight className="h-4 w-4" />
            </Link>
          </Button>
          <Button asChild size="lg" variant="outline" className="border-ember/40">
            <Link href="/onboarding">{t("howStep2Title")}</Link>
          </Button>
        </div>
      </div>
    </section>
  );
}
