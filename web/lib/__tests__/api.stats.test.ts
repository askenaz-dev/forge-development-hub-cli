import { describe, it, expect } from "vitest";
import { aggregateCatalogStats, type ComponentSummary } from "../api";

/**
 * Task 7.1 — unit-test the catalog stats aggregation against a fixture catalog.
 *
 * Mirrors the spec scenario: 5 skills, 2 rules, 1 agent, 1 hook → total 9,
 * with per-kind counts and a version total, and deprecated/yanked pinned to 0
 * (Decision D3 — lifecycle status not yet tracked).
 */

function summary(
  kind: ComponentSummary["kind"],
  name: string,
  latest_version = "1.0.0"
): ComponentSummary {
  return {
    kind,
    namespace: "askenaz",
    name,
    latest_version,
    latest_hash: "sha256:deadbeef",
    scan_status: "pass",
  };
}

const FIXTURE: ComponentSummary[] = [
  summary("skill", "design-system"),
  summary("skill", "code-review"),
  summary("skill", "release-notes"),
  summary("skill", "incident-runbook"),
  summary("skill", "api-linter"),
  summary("rule", "no-secrets"),
  summary("rule", "license-header"),
  summary("agent", "triage-bot"),
  summary("hook", "pre-commit-scan"),
];

describe("aggregateCatalogStats", () => {
  it("computes total and per-kind counts from the fixture catalog", () => {
    const stats = aggregateCatalogStats(FIXTURE);
    expect(stats.total).toBe(9);
    expect(stats.perKind.skill).toBe(5);
    expect(stats.perKind.rule).toBe(2);
    expect(stats.perKind.agent).toBe(1);
    expect(stats.perKind.hook).toBe(1);
  });

  it("counts one version per component (latest_version present)", () => {
    const stats = aggregateCatalogStats(FIXTURE);
    expect(stats.totalVersions).toBe(9);
  });

  it("pins deprecated and yanked to 0 (lifecycle not yet tracked, D3)", () => {
    const stats = aggregateCatalogStats(FIXTURE);
    expect(stats.deprecated).toBe(0);
    expect(stats.yanked).toBe(0);
  });

  it("returns all-zero counts for an empty catalog", () => {
    const stats = aggregateCatalogStats([]);
    expect(stats.total).toBe(0);
    expect(stats.totalVersions).toBe(0);
    expect(stats.perKind).toEqual({ skill: 0, rule: 0, agent: 0, hook: 0 });
    expect(stats.deprecated).toBe(0);
    expect(stats.yanked).toBe(0);
  });

  it("does not count a component missing latest_version toward totalVersions", () => {
    const withMissing: ComponentSummary[] = [
      summary("skill", "complete"),
      { ...summary("rule", "no-version"), latest_version: "" },
    ];
    const stats = aggregateCatalogStats(withMissing);
    expect(stats.total).toBe(2);
    expect(stats.totalVersions).toBe(1);
  });

  it("is pure — it does not mutate the input list", () => {
    const input = [summary("skill", "x")];
    const snapshot = JSON.parse(JSON.stringify(input));
    aggregateCatalogStats(input);
    expect(input).toEqual(snapshot);
  });
});
