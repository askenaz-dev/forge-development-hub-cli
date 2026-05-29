/**
 * Skeleton for the /skills catalog — mirrors the real grid so the layout
 * doesn't shift when content arrives. Shown while the server fetches the
 * skill list (including on every ?q= search), avoiding a frozen page.
 */
export default function SkillsLoading() {
  return (
    <div
      className="container py-12"
      role="status"
      aria-live="polite"
      aria-busy="true"
    >
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="h-9 w-48 animate-pulse rounded-md bg-muted" />
        <div className="h-10 w-full max-w-xs animate-pulse rounded-md bg-muted" />
      </div>

      <div className="mt-8 grid gap-4 md:grid-cols-2 lg:grid-cols-3">
        {Array.from({ length: 6 }).map((_, i) => (
          <div key={i} className="h-40 rounded-lg border bg-card p-6">
            <div className="h-3 w-20 animate-pulse rounded bg-muted" />
            <div className="mt-2 h-5 w-32 animate-pulse rounded bg-muted" />
            <div className="mt-4 h-3 w-full animate-pulse rounded bg-muted" />
            <div className="mt-2 h-3 w-5/6 animate-pulse rounded bg-muted" />
            <div className="mt-6 h-3 w-16 animate-pulse rounded bg-muted" />
          </div>
        ))}
      </div>
      <span className="sr-only">Loading skills…</span>
    </div>
  );
}
