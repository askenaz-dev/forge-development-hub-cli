"use client";

import * as React from "react";
import { createPortal } from "react-dom";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Menu, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

interface NavLink {
  href: string;
  label: string;
}

/**
 * MobileNav — the small-viewport navigation affordance (portal-web responsive
 * requirement). A hamburger button opens an accessible disclosure panel that
 * lists every destination plus the theme + locale controls.
 *
 * Accessibility contract:
 *   - button exposes `aria-expanded` + `aria-controls`;
 *   - `Esc` closes; focus is trapped within the panel while open and returns
 *     to the trigger on close;
 *   - body scroll is locked while open;
 *   - navigating (route change) auto-closes.
 *
 * It is rendered only below the `md` breakpoint by SiteNav. The theme toggle
 * and locale switcher are passed in as children so this component stays free
 * of their client-only dependencies and SiteNav keeps one source of truth.
 */
export function MobileNav({
  links,
  signInLabel,
  menuLabel,
  closeLabel,
  controls,
}: {
  links: NavLink[];
  signInLabel: string;
  menuLabel: string;
  closeLabel: string;
  /** theme toggle + locale switcher, rendered in the panel footer. */
  controls: React.ReactNode;
}) {
  const [open, setOpen] = React.useState(false);
  // Portal target only exists after mount (SSR has no document.body).
  const [mounted, setMounted] = React.useState(false);
  React.useEffect(() => setMounted(true), []);
  const pathname = usePathname();
  const panelRef = React.useRef<HTMLDivElement | null>(null);
  const triggerRef = React.useRef<HTMLButtonElement | null>(null);

  // Close on route change.
  React.useEffect(() => {
    setOpen(false);
  }, [pathname]);

  // Esc to close + body scroll lock + focus management while open.
  React.useEffect(() => {
    if (!open) return;
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
        return;
      }
      if (e.key === "Tab" && panelRef.current) {
        const focusables = panelRef.current.querySelectorAll<HTMLElement>(
          'a[href], button:not([disabled]), [tabindex]:not([tabindex="-1"])'
        );
        const first = focusables[0];
        const last = focusables[focusables.length - 1];
        if (!first || !last) return;
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first.focus();
        }
      }
    };
    document.addEventListener("keydown", onKeyDown);

    // Move focus into the panel.
    const t = window.setTimeout(() => {
      panelRef.current
        ?.querySelector<HTMLElement>('a[href], button:not([disabled])')
        ?.focus();
    }, 0);

    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.body.style.overflow = prevOverflow;
      window.clearTimeout(t);
      triggerRef.current?.focus();
    };
  }, [open]);

  return (
    <div className="md:hidden">
      <Button
        ref={triggerRef}
        variant="ghost"
        size="icon"
        aria-label={menuLabel}
        aria-expanded={open}
        aria-controls="mobile-nav-panel"
        onClick={() => setOpen((v) => !v)}
      >
        <Menu className="h-5 w-5" />
      </Button>

      {/* Portal the overlay to <body>. SiteNav's <header> uses backdrop-blur
          (backdrop-filter), which makes it the containing block for any
          descendant `position: fixed` — so an inline overlay would size against
          the 56px header instead of the viewport, leaving the panel only as
          tall as its title row and the nav links bleeding over the page. */}
      {open && mounted &&
        createPortal(
          <div className="fixed inset-0 z-50">
            {/* Backdrop */}
            <button
              type="button"
              aria-hidden="true"
              tabIndex={-1}
              onClick={() => setOpen(false)}
              className="absolute inset-0 bg-background/80 backdrop-blur-sm animate-fade-in"
            />
            {/* Panel */}
            <div
              id="mobile-nav-panel"
              ref={panelRef}
              role="dialog"
              aria-modal="true"
              aria-label={menuLabel}
              className="absolute right-0 top-0 flex h-full w-[min(20rem,85vw)] flex-col border-l bg-card p-4 shadow-xl animate-fade-in-up"
            >
              <div className="mb-2 flex items-center justify-between">
                <span className="font-semibold">Forge Development Hub</span>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={closeLabel}
                  onClick={() => setOpen(false)}
                >
                  <X className="h-5 w-5" />
                </Button>
              </div>

              <nav className="flex flex-col gap-1">
                {links.map((l) => {
                  const active =
                    pathname === l.href || pathname.endsWith(l.href);
                  return (
                    <Link
                      key={l.href}
                      href={l.href}
                      className={cn(
                        "rounded-md px-3 py-2.5 text-base transition-colors hover:bg-accent hover:text-accent-foreground",
                        active && "bg-accent font-medium text-accent-foreground"
                      )}
                    >
                      {l.label}
                    </Link>
                  );
                })}
              </nav>

              <div className="mt-4 border-t pt-4">
                <Button asChild className="w-full">
                  <Link href="/auth/signin">{signInLabel}</Link>
                </Button>
              </div>

              <div className="mt-auto flex items-center justify-between border-t pt-4">
                {controls}
              </div>
            </div>
          </div>,
          document.body
        )}
    </div>
  );
}
