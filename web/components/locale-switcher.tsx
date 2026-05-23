"use client";

import * as React from "react";
import { useRouter, usePathname } from "next/navigation";
import { useLocale } from "next-intl";
import { Languages } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

const LOCALES = [
  { code: "es", label: "Español" },
  { code: "en", label: "English" },
] as const;

/**
 * LocaleSwitcher rewrites the current path's locale segment. Cookie
 * persistence is handled by next-intl middleware.
 */
export function LocaleSwitcher() {
  const router = useRouter();
  const pathname = usePathname();
  const current = useLocale();

  const switchTo = (next: string) => {
    if (next === current) return;
    // Strip any current locale prefix so we have the canonical path.
    const stripped = pathname.replace(/^\/(es|en)(\/|$)/, "/");
    // Default locale (es) serves without a URL prefix; non-default
    // locales serve under /<locale>/...
    const target =
      next === "es"
        ? stripped
        : `/${next}${stripped === "/" ? "" : stripped}`;
    router.push(target);
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label="Switch language">
          <Languages className="h-5 w-5" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {LOCALES.map((l) => (
          <DropdownMenuItem key={l.code} onClick={() => switchTo(l.code)}>
            {l.label}
            {current === l.code && <span className="ml-auto text-xs">•</span>}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
