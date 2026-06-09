/**
 * No-op stand-in for the `server-only` package under unit tests.
 *
 * In a real build, `import "server-only"` is a Next.js guard that fails the
 * compile if the module is pulled into a Client Component. There is no client
 * boundary in a Node/vitest unit test, so we alias the import to this empty
 * module (see vitest.config.ts `resolve.alias`) and let `lib/bff.ts` import
 * normally. This does NOT weaken the production guard — only the test build
 * sees this shim.
 */
export {};
