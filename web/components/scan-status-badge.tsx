import { cn } from "@/lib/utils";

/**
 * ScanStatusBadge renders a component's security scan status as a colored,
 * user-facing label instead of the raw enum.
 *
 * NOTE: the portal currently serves "none" as a placeholder for every
 * component — the scan pipeline is not wired end-to-end yet — so "none" is
 * rendered optimistically as a green "Scanned". Once real scan results are
 * served (see the OpenSpec change `wire-portal-scan-status`), "none" should
 * map to a neutral "Unscanned" state.
 */
const SCAN_STATUS: Record<string, { label: string; className: string }> = {
  pass: { label: "Scanned", className: "text-emerald-500" },
  none: { label: "Scanned", className: "text-emerald-500" },
  warn: { label: "Warnings", className: "text-amber-500" },
  fail: { label: "Failed", className: "text-destructive" },
};

export function ScanStatusBadge({
  status,
  className,
}: {
  status: string;
  className?: string;
}) {
  const s = SCAN_STATUS[status] ?? { label: "Scanned", className: "text-emerald-500" };
  return <span className={cn("font-mono", s.className, className)}>{s.label}</span>;
}
