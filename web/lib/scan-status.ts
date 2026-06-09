/**
 * Scan-status presentation mapping (capability portal-scan-status).
 *
 * The portal serves the real fdh-scan verdict per component; this module maps
 * each raw status enum to the user-facing {label, className} the ScanStatusBadge
 * renders, instead of showing the raw enum:
 *   pass → green "Scanned", warn → amber "Warnings", fail → red "Failed",
 *   none → neutral "Unscanned" (not scanned / no result available).
 *
 * This is intentionally a plain (no-JSX) module so the mapping can be unit-tested
 * directly under vitest — see lib/__tests__/scan-status-badge.test.ts. The
 * <ScanStatusBadge> component imports SCAN_STATUS/scanStatusInfo from here.
 */
export const UNSCANNED = { label: "Unscanned", className: "text-muted-foreground" } as const;

export const SCAN_STATUS: Record<string, { label: string; className: string }> = {
  pass: { label: "Scanned", className: "text-emerald-500" },
  warn: { label: "Warnings", className: "text-amber-500" },
  fail: { label: "Failed", className: "text-destructive" },
  none: UNSCANNED,
};

/**
 * scanStatusInfo resolves a raw scan-status enum to the {label, className} the
 * badge displays, falling back to the neutral "Unscanned" entry for any value
 * not present in SCAN_STATUS (e.g. "", or an unrecognized status).
 */
export function scanStatusInfo(status: string): { label: string; className: string } {
  return SCAN_STATUS[status] ?? UNSCANNED;
}
