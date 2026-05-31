import { getTranslations } from "next-intl/server";
import { AGENTS } from "@/lib/landing-data";

/**
 * AgentStrip — "works with the agents you already use".
 *
 * A duplicated, marquee-scrolling row of the four supported agents on small
 * screens (the CSS `animate-marquee` keyframe; paused under reduced motion via
 * the global guard), settling into a static centered row at `md+`. Agent names
 * render as styled wordmarks (safe, license-free) rather than logos.
 */
export async function AgentStrip() {
  const t = await getTranslations("landing");

  return (
    <section className="border-b bg-muted/30 py-12">
      <div className="container">
        <h2 className="text-center text-sm font-medium uppercase tracking-wide text-muted-foreground">
          {t("agentsHeading")}
        </h2>

        {/* Static, wrapped row at md+; marquee on small screens. */}
        <div className="mt-8 hidden flex-wrap items-center justify-center gap-x-12 gap-y-4 md:flex">
          {AGENTS.map((a) => (
            <span key={a} className="text-lg font-semibold text-foreground/80">
              {a}
            </span>
          ))}
        </div>

        <div className="relative mt-8 overflow-hidden md:hidden">
          <div className="flex w-max animate-marquee items-center gap-10">
            {[...AGENTS, ...AGENTS].map((a, i) => (
              <span
                key={`${a}-${i}`}
                className="whitespace-nowrap text-lg font-semibold text-foreground/80"
              >
                {a}
              </span>
            ))}
          </div>
        </div>

        <p className="mt-6 text-center text-sm text-muted-foreground">
          {t("agentsSub")}
        </p>
      </div>
    </section>
  );
}
