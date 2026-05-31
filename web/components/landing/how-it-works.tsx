import { Download, Wrench, Zap } from "lucide-react";
import { getTranslations } from "next-intl/server";
import { CopyCommand } from "@/components/copy-command";

/**
 * HowItWorks — install → init → use, as three numbered steps connected by an
 * ember rail. Server-rendered; the only client island is the CopyCommand for
 * the install one-liner. Scroll entrance is handled by wrapping this section in
 * <Reveal> at the page level.
 */
export async function HowItWorks() {
  const t = await getTranslations("landing");

  const steps = [
    { Icon: Download, title: t("howStep1Title"), body: t("howStep1Body") },
    { Icon: Wrench, title: t("howStep2Title"), body: t("howStep2Body") },
    { Icon: Zap, title: t("howStep3Title"), body: t("howStep3Body") },
  ];

  return (
    <section className="container py-20">
      <div className="mx-auto max-w-2xl text-center">
        <h2 className="text-3xl font-bold tracking-tight">{t("howHeading")}</h2>
        <p className="mt-3 text-muted-foreground">{t("howSub")}</p>
      </div>

      <ol className="mx-auto mt-12 grid max-w-5xl gap-8 md:grid-cols-3">
        {steps.map((s, i) => (
          <li key={s.title} className="relative flex flex-col items-center text-center">
            <div className="relative flex h-14 w-14 items-center justify-center rounded-full border border-ember/40 bg-ember/10 text-ember">
              <s.Icon className="h-6 w-6" />
              <span className="absolute -right-1 -top-1 flex h-6 w-6 items-center justify-center rounded-full bg-ember text-xs font-bold text-ember-foreground">
                {i + 1}
              </span>
            </div>
            <h3 className="mt-5 text-lg font-semibold">{s.title}</h3>
            <p className="mt-2 max-w-xs text-sm text-muted-foreground">{s.body}</p>
          </li>
        ))}
      </ol>

      <div className="mx-auto mt-10 max-w-md">
        <CopyCommand command="npm i -g @askenaz-dev/fdh" />
      </div>
    </section>
  );
}
