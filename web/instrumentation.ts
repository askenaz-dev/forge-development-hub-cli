import { patchGlobalFetch } from "./lib/fetch-no-compress";

/**
 * Next.js runs `register()` once per server runtime at process startup, before
 * any request is served or any background ISR revalidation runs. We use it to
 * install the no-compression fetch shim process-wide so server-side fetches to
 * CDN-fronted hosts (the IdP, the GitHub releases API) never receive a
 * `br`/`zstd` body that Node 22 cannot decompress. See lib/fetch-no-compress.ts.
 */
export function register(): void {
  // The crashing fetches all run in the Node.js runtime (server components,
  // route handlers, ISR). Skip the Edge runtime, where undici isn't used.
  if (process.env.NEXT_RUNTIME === "nodejs") {
    patchGlobalFetch();
  }
}
