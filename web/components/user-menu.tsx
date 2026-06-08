"use client";

import Link from "next/link";
import { useTranslations } from "next-intl";
import { ChevronDown, LogOut, Shield, User } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from "@/components/ui/dropdown-menu";

/**
 * Authenticated user menu shown in the desktop header once a session exists.
 *
 * The dropdown needs client interactivity (open/close), so it's a Client
 * Component; the sign-out itself is the `signOutAction` Server Action passed in
 * from the (server) SiteNav, invoked via a progressive-enhancement form so it
 * works without any client-side auth library.
 */
export function UserMenu({
  name,
  email,
  isAdmin,
  signOutAction,
}: {
  name: string;
  email?: string;
  isAdmin: boolean;
  signOutAction: () => Promise<void>;
}) {
  const t = useTranslations("nav");

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="sm" className="gap-1.5">
          <User className="h-4 w-4" />
          <span className="max-w-[10rem] truncate">{name}</span>
          <ChevronDown className="h-3.5 w-3.5 opacity-60" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-[13rem]">
        <div className="px-2 py-1.5">
          <p className="text-xs text-muted-foreground">{t("signedInAs")}</p>
          <p className="truncate text-sm font-medium">{name}</p>
          {email ? (
            <p className="truncate text-xs text-muted-foreground">{email}</p>
          ) : null}
        </div>
        <div className="my-1 h-px bg-border" />
        {isAdmin ? (
          <DropdownMenuItem asChild>
            <Link href="/admin" className="gap-2">
              <Shield className="h-4 w-4 text-ember" />
              {t("admin")}
            </Link>
          </DropdownMenuItem>
        ) : null}
        <DropdownMenuItem asChild>
          <Link href="/profile" className="gap-2">
            <User className="h-4 w-4" />
            {t("account")}
          </Link>
        </DropdownMenuItem>
        <div className="my-1 h-px bg-border" />
        <form action={signOutAction}>
          <button
            type="submit"
            className="relative flex w-full cursor-pointer select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none transition-colors hover:bg-accent hover:text-accent-foreground"
          >
            <LogOut className="h-4 w-4" />
            {t("signOut")}
          </button>
        </form>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
