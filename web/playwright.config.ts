import { defineConfig, devices } from "@playwright/test";

/**
 * Playwright config for the portal's accessibility smoke.
 *
 * The suite assumes the web server is already up at PLAYWRIGHT_BASE_URL.
 * For CI we spin a fresh `next dev` via the `webServer` block below; for
 * local runs developers either point at their dev server (default URL) or
 * provide PLAYWRIGHT_BASE_URL when running against the dockerized stack.
 */
export default defineConfig({
  testDir: "./tests",
  // Sequential workers — the dockerized web in production mode handles
  // requests quickly, but the per-test page.goto + axe analysis serializes
  // cleanly without burning resources on parallel browser instances.
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? "http://localhost:3000",
    trace: "on-first-retry",
  },
  projects: [
    {
      name: "chromium",
      // Emulate reduced motion so the accessibility audit evaluates the
      // settled, final visual state. Scroll-reveal/count-up enhancements
      // render immediately under prefers-reduced-motion (see the motion
      // primitives), so this is exactly what a reduced-motion user sees —
      // and it avoids axe sampling an element mid opacity-fade transition.
      use: { ...devices["Desktop Chrome"], reducedMotion: "reduce" },
    },
  ],
  webServer: process.env.PLAYWRIGHT_BASE_URL
    ? undefined
    : {
        command: "pnpm dev",
        url: "http://localhost:3000",
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
        env: { PORT: "3000" },
      },
});
