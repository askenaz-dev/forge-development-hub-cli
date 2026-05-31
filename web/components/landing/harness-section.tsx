import { Package } from "lucide-react";
import { getTranslations } from "next-intl/server";

/**
 * HarnessSection — explains the harness (a curated bundle of components) with a
 * small illustrative manifest snippet. Server-rendered.
 */
export async function HarnessSection() {
  const t = await getTranslations("landing");

  return (
    <section className="border-t bg-muted/30 py-20">
      <div className="container grid items-center gap-10 md:grid-cols-2">
        <div>
          <div className="inline-flex h-12 w-12 items-center justify-center rounded-lg border border-ember/40 bg-ember/10 text-ember">
            <Package className="h-6 w-6" />
          </div>
          <h2 className="mt-5 text-3xl font-bold tracking-tight">
            {t("harnessHeading")}
          </h2>
          <p className="mt-4 max-w-md text-muted-foreground">{t("harnessBody")}</p>
        </div>

        <div className="rounded-lg border bg-card p-5 font-mono text-sm shadow-sm">
          <p className="text-muted-foreground"># .fdh/manifest.yaml</p>
          <p>
            <span className="text-ember">harness</span>
            <span className="text-foreground">: </span>
            <span className="text-foreground">forge-frontend</span>
          </p>
          <p className="text-foreground">extends:</p>
          <p className="pl-4 text-foreground">
            add_skills: [<span className="text-ember">i18n-helper</span>]
          </p>
          <p className="pl-4 text-foreground">
            remove_rules: [<span className="text-ember">no-console-log</span>]
          </p>
        </div>
      </div>
    </section>
  );
}
