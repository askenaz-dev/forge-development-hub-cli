import Link from "next/link";
import { Sparkles } from "lucide-react";
import { getTranslations } from "next-intl/server";
import { ThemeToggle } from "@/components/theme-toggle";
import { Button } from "@/components/ui/button";
import { LocaleSwitcher } from "@/components/locale-switcher";

/**
 * SiteNav is the global navigation header used by every page.
 *
 * Renders server-side (the only client islands are ThemeToggle and
 * LocaleSwitcher). The four primitive kinds each get a nav tab so the whole
 * catalog is discoverable, not just skills.
 */
export async function SiteNav() {
  const t = await getTranslations("nav");
  return (
    <header className="sticky top-0 z-40 w-full border-b bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60">
      <div className="container flex h-14 items-center justify-between">
        <Link href="/" className="flex items-center gap-2 font-semibold">
          <Sparkles className="h-5 w-5 text-primary" />
          <span>FDH</span>
          <span className="hidden text-muted-foreground sm:inline">
            forge Development Hub
          </span>
        </Link>

        <nav className="flex items-center gap-1 text-sm">
          <Button asChild variant="ghost" size="sm">
            <Link href="/skills">{t("skills")}</Link>
          </Button>
          <Button asChild variant="ghost" size="sm">
            <Link href="/rules">{t("rules")}</Link>
          </Button>
          <Button asChild variant="ghost" size="sm">
            <Link href="/agents">{t("agents")}</Link>
          </Button>
          <Button asChild variant="ghost" size="sm">
            <Link href="/hooks">{t("hooks")}</Link>
          </Button>
          <span className="mx-2 hidden h-6 w-px bg-border md:inline-block" />
          <Button asChild variant="ghost" size="sm">
            <Link href="/install">{t("install")}</Link>
          </Button>
          <Button asChild variant="ghost" size="sm">
            <Link href="/onboarding">{t("getStarted")}</Link>
          </Button>
          <LocaleSwitcher />
          <ThemeToggle />
          <Button asChild variant="default" size="sm" className="ml-1">
            <Link href="/auth/signin">{t("signIn")}</Link>
          </Button>
        </nav>
      </div>
    </header>
  );
}
