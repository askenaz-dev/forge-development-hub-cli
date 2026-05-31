import Link from "next/link";
import { Flame } from "lucide-react";
import { getTranslations } from "next-intl/server";
import { ThemeToggle } from "@/components/theme-toggle";
import { Button } from "@/components/ui/button";
import { LocaleSwitcher } from "@/components/locale-switcher";
import { MobileNav } from "@/components/mobile-nav";

/**
 * SiteNav is the global navigation header used by every page.
 *
 * Renders server-side (the only client islands are ThemeToggle,
 * LocaleSwitcher, and MobileNav). The four primitive kinds each get a nav tab
 * so the whole catalog is discoverable, not just skills.
 *
 * Responsive: at `lg+` the full horizontal nav shows; below `lg` (incl. tablet
 * widths like 768px) it collapses to a hamburger (`<MobileNav>`). The full nav
 * needs ~1024px to fit, so `md` (768px) would overflow — hence `lg`, not `md`
 * (portal-web responsive requirement).
 */
export async function SiteNav() {
  const t = await getTranslations("nav");

  const links = [
    { href: "/skills", label: t("skills") },
    { href: "/rules", label: t("rules") },
    { href: "/agents", label: t("agents") },
    { href: "/hooks", label: t("hooks") },
    { href: "/install", label: t("install") },
    { href: "/onboarding", label: t("getStarted") },
  ];

  return (
    <header className="sticky top-0 z-40 w-full border-b bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60">
      <div className="container flex h-14 items-center justify-between">
        <Link href="/" className="flex items-center gap-2 font-semibold">
          <Flame className="h-5 w-5 text-ember" />
          <span>FDH</span>
          <span className="hidden text-muted-foreground sm:inline">
            Forge Development Hub
          </span>
        </Link>

        {/* Desktop nav */}
        <nav className="hidden items-center gap-1 text-sm lg:flex">
          {links.map((l) => (
            <Button key={l.href} asChild variant="ghost" size="sm">
              <Link href={l.href}>{l.label}</Link>
            </Button>
          ))}
          <span className="mx-2 h-6 w-px bg-border" />
          <LocaleSwitcher />
          <ThemeToggle />
          <Button asChild variant="default" size="sm" className="ml-1">
            <Link href="/auth/signin">{t("signIn")}</Link>
          </Button>
        </nav>

        {/* Mobile/tablet nav (below lg) */}
        <MobileNav
          links={links}
          signInLabel={t("signIn")}
          menuLabel={t("openMenu")}
          closeLabel={t("closeMenu")}
          controls={
            <>
              <LocaleSwitcher />
              <ThemeToggle />
            </>
          }
        />
      </div>
    </header>
  );
}
