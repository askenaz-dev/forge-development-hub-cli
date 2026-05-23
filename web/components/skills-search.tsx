"use client";

import * as React from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Search } from "lucide-react";
import { Input } from "@/components/ui/input";

/**
 * SkillsSearch is the client island on /skills.
 *
 * Behavior: debounces user input by 250ms, then pushes the new `?q=` into
 * the URL so the server-rendered list re-fetches. The URL is the source
 * of truth — back/forward navigation works, and the URL is shareable.
 */
export function SkillsSearch({
  initialQuery,
  placeholder,
}: {
  initialQuery: string;
  placeholder: string;
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
      router.push(qs ? `/skills?${qs}` : "/skills");
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
        aria-label="Search skills"
      />
    </div>
  );
}
