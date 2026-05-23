import Link from "next/link";
import { getTranslations } from "next-intl/server";
import { ArrowRight, Boxes, Lock, Sparkles } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

/**
 * Landing page — the entry point for new visitors.
 *
 * Three CTAs above the fold (Install CLI, Browse skills, Sign in) per the
 * portal-onboarding spec. The feature trio communicates the product's
 * three load-bearing values.
 */
export default async function LandingPage() {
  const t = await getTranslations("landing");

  return (
    <>
      <section className="container py-16 md:py-24">
        <div className="mx-auto max-w-3xl text-center">
          <h1 className="text-balance text-4xl font-bold tracking-tight md:text-5xl">
            {t("headline")}
          </h1>
          <p className="mt-6 text-balance text-lg text-muted-foreground">
            {t("subhead")}
          </p>
          <div className="mt-10 flex flex-col items-center justify-center gap-3 sm:flex-row">
            <Button asChild size="lg">
              <Link href="/install">
                {t("ctaInstall")} <ArrowRight className="h-4 w-4" />
              </Link>
            </Button>
            <Button asChild size="lg" variant="outline">
              <Link href="/skills">{t("ctaBrowse")}</Link>
            </Button>
            <Button asChild size="lg" variant="ghost">
              <Link href="/auth/signin">{t("ctaSignIn")}</Link>
            </Button>
          </div>
        </div>
      </section>

      <section className="container pb-20">
        <div className="grid gap-6 md:grid-cols-3">
          <Card>
            <CardHeader>
              <Boxes className="h-6 w-6 text-primary" />
              <CardTitle>{t("feature1Title")}</CardTitle>
            </CardHeader>
            <CardContent>
              <CardDescription>{t("feature1Body")}</CardDescription>
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <Lock className="h-6 w-6 text-primary" />
              <CardTitle>{t("feature2Title")}</CardTitle>
            </CardHeader>
            <CardContent>
              <CardDescription>{t("feature2Body")}</CardDescription>
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <Sparkles className="h-6 w-6 text-primary" />
              <CardTitle>{t("feature3Title")}</CardTitle>
            </CardHeader>
            <CardContent>
              <CardDescription>{t("feature3Body")}</CardDescription>
            </CardContent>
          </Card>
        </div>
      </section>
    </>
  );
}
