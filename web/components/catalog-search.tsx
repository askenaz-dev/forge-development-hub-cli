"use client";

import * as React from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Search } from "lucide-react";
import { Input } from "@/components/ui/input";

/**
 * CatalogSearch is the client search island shared by every kind's browse
 * page. It debounces input by 250ms then pushes `?q=` onto `basePath` (e.g.
 * "/rules"), keeping the URL the source of truth so the server-rendered list
 * re-fetches and the URL stays shareable.
 */
export function CatalogSearch({
  basePath,
  initialQuery,
  placeholder,
  ariaLabel,
}: {
  basePath: string;
  initialQuery: string;
  placeholder: string;
  ariaLabel: string;
}) {
  const router = useRouter();
  const params = useSearchParams();
  const [value, setValue] = React.useState(initialQuery);

  React.useEffect(() => {
    const t = window.setTimeout(() => {
      const next = new URLSearchParams(params.toString());
      if (value) {
        next.set("q", value);
      } else {
        next.delete("q");
      }
      const qs = next.toString();
      router.push(qs ? `${basePath}?${qs}` : basePath);
    }, 250);
    return () => window.clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value]);

  return (
    <div className="relative w-full max-w-md">
      <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        type="search"
        placeholder={placeholder}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        className="pl-9"
        aria-label={ariaLabel}
      />
    </div>
  );
}
