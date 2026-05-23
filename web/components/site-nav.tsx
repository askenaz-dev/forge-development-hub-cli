import Link from "next/link";
import { Sparkles } from "lucide-react";
import { ThemeToggle } from "@/components/theme-toggle";
import { Button } from "@/components/ui/button";
import { LocaleSwitcher } from "@/components/locale-switcher";

/**
 * SiteNav is the global navigation header used by every page.
 *
 * The component renders entirely server-side; the only client islands
 * are ThemeToggle and LocaleSwitcher.
 */
export function SiteNav() {
  return (
    <header className="sticky top-0 z-40 w-full border-b bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60">
      <div className="container flex h-14 items-center justify-between">
        <Link href="/" className="flex items-center gap-2 font-semibold">
          <Sparkles className="h-5 w-5 text-primary" />
          <span>FDH</span>
          <span className="hidden text-muted-foreground sm:inline">
            Falabella Development Hub
          </span>
        </Link>

        <nav className="flex items-center gap-1 text-sm">
          <Button asChild variant="ghost" size="sm">
            <Link href="/skills">Skills</Link>
          </Button>
          <Button asChild variant="ghost" size="sm">
            <Link href="/install">Install CLI</Link>
          </Button>
          <Button asChild variant="ghost" size="sm">
            <Link href="/onboarding">Get started</Link>
          </Button>
          <span className="mx-2 hidden h-6 w-px bg-border md:inline-block" />
          <LocaleSwitcher />
          <ThemeToggle />
          <Button asChild variant="default" size="sm" className="ml-1">
            <Link href="/auth/signin">Sign in</Link>
          </Button>
        </nav>
      </div>
    </header>
  );
}
