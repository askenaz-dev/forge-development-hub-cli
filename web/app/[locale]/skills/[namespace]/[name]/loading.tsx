/**
 * Skeleton for a skill detail page, shown while the server fetches the
 * skill manifest/README so navigation feels instant rather than frozen.
 */
export default function SkillDetailLoading() {
  return (
    <div
      className="container max-w-3xl py-12"
      role="status"
      aria-live="polite"
      aria-busy="true"
    >
      <div className="h-4 w-24 animate-pulse rounded bg-muted" />
      <div className="mt-3 h-8 w-2/3 animate-pulse rounded bg-muted" />
      <div className="mt-4 flex gap-2">
        <div className="h-6 w-16 animate-pulse rounded-full bg-muted" />
        <div className="h-6 w-20 animate-pulse rounded-full bg-muted" />
      </div>
      <div className="mt-8 space-y-2">
        {Array.from({ length: 8 }).map((_, i) => (
          <div key={i} className="h-3 w-full animate-pulse rounded bg-muted" />
        ))}
      </div>
      <span className="sr-only">Loading skill…</span>
    </div>
  );
}
