import { defineConfig } from "vitest/config";

/**
 * Vitest config for the portal's pure unit tests (`lib/__tests__/**`).
 *
 * Scope is deliberately narrow:
 *   - `include` covers ONLY `lib/__tests__` so vitest never collects the
 *     Playwright a11y specs under `tests/` (those use @playwright/test's own
 *     `test`/`expect` and run via `pnpm a11y`, a different runner).
 *   - `server-only` is aliased to an empty module. That bare import is a
 *     Next.js bundler guard that errors only in a Client Component build; under
 *     a Node/vitest unit test there is no client boundary, so resolving it to a
 *     no-op lets us unit-test `lib/bff.ts` (which guards itself with
 *     `import "server-only"`) without pulling in the Next build pipeline.
 */
export default defineConfig({
  test: {
    include: ["lib/__tests__/**/*.test.ts"],
    environment: "node",
    globals: false,
  },
  resolve: {
    alias: {
      "server-only": new URL("./lib/__tests__/__mocks__/server-only.ts", import.meta.url)
        .pathname,
    },
  },
});
