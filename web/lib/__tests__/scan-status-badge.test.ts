import { describe, it, expect } from "vitest";
import { SCAN_STATUS, scanStatusInfo } from "../scan-status";

/**
 * Task 7.5 — unit-test the scan-status badge's status → label mapping.
 *
 * Asserts the exact user-facing labels the <ScanStatusBadge> renders (capability
 * portal-scan-status): pass → "Scanned", warn → "Warnings", fail → "Failed",
 * none → "Unscanned", with any unrecognized status falling back to "Unscanned".
 *
 * The mapping is the pure module-level map (SCAN_STATUS) resolved via
 * scanStatusInfo() in @/lib/scan-status — the same map the component renders.
 * Testing that helper directly (vitest, node env) needs no DOM/render
 * dependency.
 */
describe("scanStatusInfo", () => {
  it("maps each scan status to its exact user-facing label", () => {
    expect(scanStatusInfo("pass").label).toBe("Scanned");
    expect(scanStatusInfo("warn").label).toBe("Warnings");
    expect(scanStatusInfo("fail").label).toBe("Failed");
    expect(scanStatusInfo("none").label).toBe("Unscanned");
  });

  it("falls back to 'Unscanned' for an empty or unrecognized status", () => {
    expect(scanStatusInfo("").label).toBe("Unscanned");
    expect(scanStatusInfo("bogus").label).toBe("Unscanned");
    expect(scanStatusInfo("PASS").label).toBe("Unscanned"); // case-sensitive enum
  });

  it("carries the status-specific className for known statuses", () => {
    expect(scanStatusInfo("pass").className).toBe("text-emerald-500");
    expect(scanStatusInfo("warn").className).toBe("text-amber-500");
    expect(scanStatusInfo("fail").className).toBe("text-destructive");
    expect(scanStatusInfo("none").className).toBe("text-muted-foreground");
  });
});

describe("SCAN_STATUS map", () => {
  it("covers exactly the four scan-status enum values", () => {
    expect(Object.keys(SCAN_STATUS).sort()).toEqual(["fail", "none", "pass", "warn"]);
  });

  it("maps each enum value to its exact label", () => {
    expect(SCAN_STATUS.pass!.label).toBe("Scanned");
    expect(SCAN_STATUS.warn!.label).toBe("Warnings");
    expect(SCAN_STATUS.fail!.label).toBe("Failed");
    expect(SCAN_STATUS.none!.label).toBe("Unscanned");
  });
});
