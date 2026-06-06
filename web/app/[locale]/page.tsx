import { setRequestLocale } from "next-intl/server";
import { Hero } from "@/components/landing/hero";
import { AgentStrip } from "@/components/landing/agent-strip";
import { PrimitivesSection } from "@/components/landing/primitives-section";
import { HowItWorks } from "@/components/landing/how-it-works";
import { LiveCatalog } from "@/components/landing/live-catalog";
import { TrustBand } from "@/components/landing/trust-band";
import { HarnessSection } from "@/components/landing/harness-section";
import { FinalCta } from "@/components/landing/final-cta";
import { Reveal } from "@/components/motion/reveal";
import { getLandingData } from "@/lib/landing-data";

/**
 * Landing page — the Ember Forge narrative home.
 *
 * A scroll story that *demonstrates* the product (portal-web "narrative landing
 * with live catalog data"): hero → agents → the four primitives → how it works
 * → live catalog → trust → harness → final CTA. Sign-in lives in the global
 * header, not duplicated here (portal-onboarding CTA contract).
 *
 * Server-rendered with live ISR data (getLandingData), so first paint contains
 * real content and the page degrades gracefully if the catalog API is down.
 * Each section below the fold is wrapped in <Reveal> for an on-scroll entrance
 * that is disabled under prefers-reduced-motion.
 */
// Render dynamically so the hero's live per-kind counts always reflect the
// current catalog. (Static pre-render happens in CI where the API is
// unreachable, which would otherwise bake the API-down fallback into the page
// until the first ISR revalidation.)
export const dynamic = "force-dynamic";

export default async function LandingPage({
  params,
}: {
  params: Promise<{ locale: string }>;
}) {
  const { locale } = await params;
  setRequestLocale(locale);

  const data = await getLandingData();

  return (
    <>
      <Hero data={data} />

      <Reveal>
        <AgentStrip />
      </Reveal>

      <Reveal>
        <PrimitivesSection />
      </Reveal>

      <Reveal>
        <HowItWorks />
      </Reveal>

      {/* LiveCatalog manages its own per-card staggered reveals. */}
      <LiveCatalog data={data} />

      <Reveal>
        <TrustBand />
      </Reveal>

      <Reveal>
        <HarnessSection />
      </Reveal>

      <FinalCta />
    </>
  );
}
