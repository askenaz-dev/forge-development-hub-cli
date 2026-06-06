import { cn } from "@/lib/utils";

/**
 * ScanStatusBadge renders a component's security scan status as a colored,
 * user-facing label instead of the raw enum. The portal serves the real
 * fdh-scan verdict per component (capability portal-scan-status):
 *   pass → green "Scanned", warn → amber "Warnings", fail → red "Failed",
 *   none → neutral "Unscanned" (not scanned / no result available).
 */
const UNSCANNED = { label: "Unscanned", className: "text-muted-foreground" };

const SCAN_STATUS: Record<string, { label: string; className: string }> = {
  pass: { label: "Scanned", className: "text-emerald-500" },
  warn: { label: "Warnings", className: "text-amber-500" },
  fail: { label: "Failed", className: "text-destructive" },
  none: UNSCANNED,
};

export function ScanStatusBadge({
  status,
  className,
}: {
  status: string;
  className?: string;
}) {
  const s = SCAN_STATUS[status] ?? UNSCANNED;
  return <span className={cn("font-mono", s.className, className)}>{s.label}</span>;
}
