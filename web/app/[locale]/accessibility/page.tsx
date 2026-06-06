import Link from "next/link";
import { getTranslations } from "next-intl/server";
import { ExternalLink } from "lucide-react";
import { Card, CardContent } from "@/components/ui/card";

/**
 * /accessibility — the portal's accessibility statement.
 *
 * Linked from the global footer. Server-rendered and fully localized.
 */
export default async function AccessibilityPage() {
  const t = await getTranslations("accessibility");

  const features = [t("feat1"), t("feat2"), t("feat3"), t("feat4"), t("feat5")];

  return (
    <div className="container py-12">
      <header className="mx-auto max-w-3xl">
        <h1 className="text-3xl font-bold tracking-tight">{t("title")}</h1>
        <p className="mt-3 text-muted-foreground">{t("intro")}</p>
      </header>

      <div className="mx-auto mt-10 max-w-3xl space-y-8">
        <section>
          <h2 className="text-xl font-semibold tracking-tight">
            {t("commitmentHeading")}
          </h2>
          <p className="mt-2 text-muted-foreground">{t("commitmentBody")}</p>
        </section>

        <section>
          <h2 className="text-xl font-semibold tracking-tight">
            {t("featuresHeading")}
          </h2>
          <ul className="mt-3 list-disc space-y-2 pl-5 text-muted-foreground">
            {features.map((f, i) => (
              <li key={i}>{f}</li>
            ))}
          </ul>
        </section>

        <section>
          <h2 className="text-xl font-semibold tracking-tight">
            {t("feedbackHeading")}
          </h2>
          <Card className="mt-3">
            <CardContent className="pt-6">
              <p className="text-muted-foreground">{t("feedbackBody")}</p>
              <Link
                href="https://github.com/askenaz-dev/forge-development-hub/issues/new"
                target="_blank"
                rel="noreferrer noopener"
                className="mt-4 inline-flex items-center gap-1.5 text-sm font-medium text-ember hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                {t("reportIssue")}
                <ExternalLink className="h-4 w-4" aria-hidden="true" />
              </Link>
            </CardContent>
          </Card>
        </section>
      </div>
    </div>
  );
}
