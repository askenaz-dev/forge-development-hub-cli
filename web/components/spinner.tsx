/**
 * Spinner — a dependency-free, accessible loading indicator.
 *
 * Uses Tailwind's built-in `animate-spin`. Rendered by route-level
 * `loading.tsx` files (and anywhere an inline busy state is needed) so the
 * UI shows immediate feedback instead of appearing frozen during data fetches.
 */
export function Spinner({
  className = "",
  label = "Loading",
}: {
  className?: string;
  label?: string;
}) {
  return (
    <svg
      className={`animate-spin text-primary ${className}`}
      xmlns="http://www.w3.org/2000/svg"
      fill="none"
      viewBox="0 0 24 24"
      role="status"
      aria-label={label}
    >
      <circle
        className="opacity-25"
        cx="12"
        cy="12"
        r="10"
        stroke="currentColor"
        strokeWidth="4"
      />
      <path
        className="opacity-75"
        fill="currentColor"
        d="M4 12a8 8 0 0 1 8-8V0C5.373 0 0 5.373 0 12h4z"
      />
    </svg>
  );
}
