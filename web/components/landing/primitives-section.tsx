import dynamic from "next/dynamic";
import { getTranslations } from "next-intl/server";

/**
 * PrimitivesSection — server wrapper for the four primitive cards.
 *
 * The interactive (framer-motion) PrimitiveCards is loaded via next/dynamic so
 * the animation library is code-split OFF the landing's critical bundle
 * (portal-motion-system requirement). `ssr: true` keeps the card content in the
 * server-rendered first paint; the tilt enhancement hydrates from a separate
 * chunk. The section heading + copy are plain server markup.
 */
const PrimitiveCards = dynamic(() => import("./primitive-cards"), { ssr: true });

export async function PrimitivesSection() {
  const t = await getTranslations("landing");

  return (
    <section className="container py-20">
      <div className="mx-auto max-w-2xl text-center">
        <h2 className="text-3xl font-bold tracking-tight">{t("primitivesHeading")}</h2>
        <p className="mt-3 text-muted-foreground">{t("primitivesSub")}</p>
      </div>

      <div className="mx-auto mt-12 max-w-5xl">
        <PrimitiveCards
          copy={{
            skillTitle: t("primSkillTitle"),
            skillBody: t("primSkillBody"),
            ruleTitle: t("primRuleTitle"),
            ruleBody: t("primRuleBody"),
            agentTitle: t("primAgentTitle"),
            agentBody: t("primAgentBody"),
            hookTitle: t("primHookTitle"),
            hookBody: t("primHookBody"),
          }}
        />
      </div>
    </section>
  );
}
