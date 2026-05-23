import { test, expect, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

/**
 * Accessibility smoke for every public portal page.
 *
 * Strategy: visit each page, wait for the network to settle, run axe-core
 * against the rendered DOM with WCAG 2.0 A + AA rules, and fail the test
 * if any `serious` or `critical` violation is reported.
 *
 * Auth-gated pages (`/profile`, `/admin`) are excluded — they require a
 * real Keycloak session in the runner, which is out of scope for this gate.
 */

const PUBLIC_PAGES = [
  { path: "/", label: "landing-es" },
  { path: "/en", label: "landing-en" },
  { path: "/install", label: "install" },
  { path: "/skills", label: "browse" },
  { path: "/skills/security/owasp-quick-review", label: "skill-detail" },
  { path: "/onboarding", label: "onboarding" },
] as const;

async function runAxe(page: Page) {
  return new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa"])
    .analyze();
}

for (const { path, label } of PUBLIC_PAGES) {
  test(`a11y @a11y ${label} (${path})`, async ({ page }) => {
    // Use domcontentloaded — Next.js dev keeps HMR sockets open so
    // networkidle never settles. domcontentloaded + an explicit wait
    // for main content is enough for axe-core to analyze the page.
    await page.goto(path, { waitUntil: "domcontentloaded" });
    await page.waitForSelector("main", { timeout: 30_000 });
    const results = await runAxe(page);
    const blocking = results.violations.filter(
      (v) => v.impact === "serious" || v.impact === "critical"
    );
    // Always emit the report so CI logs let us see warnings even on pass.
    if (results.violations.length > 0) {
      console.warn(
        `\n[a11y] ${label} (${path}) — ${results.violations.length} total violation(s):`
      );
      for (const v of results.violations) {
        console.warn(`  - [${v.impact}] ${v.id}: ${v.help}`);
      }
    }
    expect(
      blocking,
      `serious/critical a11y violations on ${path}:\n` +
        blocking
          .map((v) => `  - ${v.id}: ${v.help} (${v.nodes.length} node(s))`)
          .join("\n")
    ).toEqual([]);
  });
}
