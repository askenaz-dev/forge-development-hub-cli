/**
 * Force `Accept-Encoding: identity` on every outbound `fetch`.
 *
 * Node 22's fetch/undici cannot decompress `br`/`zstd` responses through its
 * `DecompressionStream` path — it throws
 * "controller[kState].transformAlgorithm is not a function". CDN-fronted
 * upstreams negotiate `zstd` by default (verified: the IdP behind Cloudflare
 * and the GitHub releases API both return `content-encoding: zstd`), so any
 * server-side fetch to them crashes. The failure also surfaces from background
 * ISR revalidations that never run through middleware, so the patch must be
 * installed process-wide at startup (via `instrumentation.ts`), not only when
 * an auth route imports `auth.ts`.
 *
 * Asking for an uncompressed response sidesteps the broken decompression
 * entirely. In-cluster API calls are already uncompressed, so forcing identity
 * there is a no-op. Idempotent — safe to call from multiple entry points.
 */
const FLAG = "__fdhFetchNoCompressPatched";

export function patchGlobalFetch(): void {
  const g = globalThis as typeof globalThis & Record<string, boolean>;
  if (typeof g.fetch !== "function" || g[FLAG]) return;
  const orig = g.fetch.bind(g);
  g.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
    try {
      const headers = new Headers(
        init?.headers ?? (input instanceof Request ? input.headers : undefined)
      );
      headers.set("accept-encoding", "identity");
      return orig(input, { ...init, headers });
    } catch {
      // Never let the shim break a request; fall back to the original fetch.
      return orig(input as RequestInfo | URL, init);
    }
  }) as typeof fetch;
  g[FLAG] = true;
}
