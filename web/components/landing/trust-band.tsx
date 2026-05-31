import { ScanLine, FileSignature, KeyRound } from "lucide-react";
import { getTranslations } from "next-intl/server";

/**
 * TrustBand — the governance story (scan status, signed bundles, SSO/roles).
 * Server-rendered; scroll entrance handled by <Reveal> at the page level.
 */
export async function TrustBand() {
  const t = await getTranslations("landing");

  const items = [
    { Icon: ScanLine, title: t("trustScanTitle"), body: t("trustScanBody") },
    { Icon: FileSignature, title: t("trustSignTitle"), body: t("trustSignBody") },
    { Icon: KeyRound, title: t("trustSsoTitle"), body: t("trustSsoBody") },
  ];

  return (
    <section className="container py-20">
      <div className="mx-auto max-w-2xl text-center">
        <h2 className="text-3xl font-bold tracking-tight">{t("trustHeading")}</h2>
        <p className="mt-3 text-muted-foreground">{t("trustSub")}</p>
      </div>

      <div className="mx-auto mt-12 grid max-w-4xl gap-6 md:grid-cols-3">
        {items.map((it) => (
          <div
            key={it.title}
            className="rounded-lg border bg-card p-6 text-card-foreground"
          >
            <it.Icon className="h-7 w-7 text-ember" />
            <h3 className="mt-4 text-lg font-semibold tracking-tight">{it.title}</h3>
            <p className="mt-2 text-sm text-muted-foreground">{it.body}</p>
          </div>
        ))}
      </div>
    </section>
  );
}
