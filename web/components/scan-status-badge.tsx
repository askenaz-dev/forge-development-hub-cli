import { cn } from "@/lib/utils";
import { scanStatusInfo } from "@/lib/scan-status";

/**
 * ScanStatusBadge renders a component's security scan status as a colored,
 * user-facing label instead of the raw enum. The portal serves the real
 * fdh-scan verdict per component (capability portal-scan-status):
 *   pass → green "Scanned", warn → amber "Warnings", fail → red "Failed",
 *   none → neutral "Unscanned" (not scanned / no result available).
 *
 * The status → {label, className} mapping lives in @/lib/scan-status (a plain,
 * no-JSX module) so it can be unit-tested directly under vitest.
 */
export function ScanStatusBadge({
  status,
  className,
}: {
  status: string;
  className?: string;
}) {
  const s = scanStatusInfo(status);
  return <span className={cn("font-mono", s.className, className)}>{s.label}</span>;
}
