import { Spinner } from "@/components/spinner";

/**
 * Global route-loading fallback for every page under /[locale].
 *
 * Next.js renders this instantly when a navigation triggers a server-side
 * data fetch, so the previous screen never appears frozen. Route segments
 * with their own loading.tsx (e.g. /skills) override this with a tailored
 * skeleton.
 */
export default function Loading() {
  return (
    <div
      className="container flex min-h-[50vh] flex-col items-center justify-center gap-3 py-12"
      role="status"
      aria-live="polite"
      aria-busy="true"
    >
      <Spinner className="h-8 w-8" />
      <p className="text-sm text-muted-foreground">Loading…</p>
    </div>
  );
}
