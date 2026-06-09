import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";

/**
 * Task 7.2 — unit-test the BFF service-token helper.
 *
 * Asserts that getServiceToken():
 *   - mints a Keycloak client-credentials token from AUTH_KEYCLOAK_ID /
 *     AUTH_KEYCLOAK_SECRET against `<issuer>/protocol/openid-connect/token`,
 *   - returns a token suitable for an `Authorization: Bearer` header,
 *   - caches the token (no second network call until near expiry) and re-mints
 *     once the cached token is about to expire,
 *   - never leaks the client secret or the token to a client-visible path:
 *     no console output carries the secret/token, and an error response does
 *     not echo the request body (which holds the secret).
 */

const ISSUER = "https://idp.example.com/realms/askenaz";
const TOKEN_ENDPOINT = `${ISSUER}/protocol/openid-connect/token`;
const CLIENT_ID = "fdh-portal";
const CLIENT_SECRET = "s3cr3t-do-not-leak";

let fetchMock: ReturnType<typeof vi.fn>;

function tokenResponse(accessToken: string, expiresIn = 300): Response {
  return new Response(
    JSON.stringify({ access_token: accessToken, expires_in: expiresIn, token_type: "Bearer" }),
    { status: 200, headers: { "content-type": "application/json" } }
  );
}

beforeEach(async () => {
  process.env.AUTH_KEYCLOAK_ISSUER = ISSUER;
  process.env.AUTH_KEYCLOAK_ID = CLIENT_ID;
  process.env.AUTH_KEYCLOAK_SECRET = CLIENT_SECRET;

  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);

  // Reset the module-scoped token cache between cases. Imported dynamically so
  // the `import "server-only"` guard (aliased to a no-op in vitest.config.ts)
  // resolves against the fresh env above.
  const { __resetServiceTokenCacheForTests } = await import("../bff");
  __resetServiceTokenCacheForTests();
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("getServiceToken", () => {
  it("POSTs a client-credentials grant to the issuer-derived token endpoint", async () => {
    fetchMock.mockResolvedValueOnce(tokenResponse("tok-1"));
    const { getServiceToken } = await import("../bff");

    const token = await getServiceToken();
    expect(token).toBe("tok-1");

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe(TOKEN_ENDPOINT);
    expect((init as RequestInit).method).toBe("POST");

    // The body carries the client-credentials grant + client id/secret.
    const body = (init as RequestInit).body as URLSearchParams;
    const params = new URLSearchParams(body.toString());
    expect(params.get("grant_type")).toBe("client_credentials");
    expect(params.get("client_id")).toBe(CLIENT_ID);
    expect(params.get("client_secret")).toBe(CLIENT_SECRET);

    // Mirrors auth.ts's OIDC fetch posture (Node-22 zstd workaround).
    expect((init as RequestInit).cache).toBe("no-store");
    const headers = new Headers((init as RequestInit).headers);
    expect(headers.get("accept-encoding")).toBe("identity");
    expect(headers.get("content-type")).toBe("application/x-www-form-urlencoded");
  });

  it("produces a token usable as an Authorization: Bearer value", async () => {
    fetchMock.mockResolvedValueOnce(tokenResponse("tok-bearer"));
    const { getServiceToken } = await import("../bff");

    const token = await getServiceToken();
    const authHeader = `Bearer ${token}`;
    expect(authHeader).toBe("Bearer tok-bearer");
  });

  it("caches the token: a second call within its lifetime does not re-mint", async () => {
    fetchMock.mockResolvedValueOnce(tokenResponse("tok-cached", 300));
    const { getServiceToken } = await import("../bff");

    const a = await getServiceToken();
    const b = await getServiceToken();
    expect(a).toBe("tok-cached");
    expect(b).toBe("tok-cached");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("re-mints when the cached token is about to expire", async () => {
    // expires_in below the 30s skew → the cached window is the 5s floor; advance
    // past it and the next call must re-mint.
    fetchMock
      .mockResolvedValueOnce(tokenResponse("tok-old", 10))
      .mockResolvedValueOnce(tokenResponse("tok-new", 300));

    // Freeze time BEFORE the first mint so the cache's notAfterMs is computed
    // against the fake clock we then advance.
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-06-08T00:00:00.000Z"));
    try {
      const { getServiceToken } = await import("../bff");
      const first = await getServiceToken();
      expect(first).toBe("tok-old");
      // Advance past the 5s usable floor so the cache is considered stale.
      vi.advanceTimersByTime(6_000);
      const second = await getServiceToken();
      expect(second).toBe("tok-new");
      expect(fetchMock).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it("never logs the client secret or the minted token", async () => {
    const spies = [
      vi.spyOn(console, "log").mockImplementation(() => {}),
      vi.spyOn(console, "info").mockImplementation(() => {}),
      vi.spyOn(console, "warn").mockImplementation(() => {}),
      vi.spyOn(console, "error").mockImplementation(() => {}),
      vi.spyOn(console, "debug").mockImplementation(() => {}),
    ];
    fetchMock.mockResolvedValueOnce(tokenResponse("tok-secretcheck"));
    const { getServiceToken } = await import("../bff");

    await getServiceToken();

    const allLogged = spies
      .flatMap((s) => s.mock.calls)
      .flat()
      .map((arg) => (typeof arg === "string" ? arg : JSON.stringify(arg)))
      .join("\n");
    expect(allLogged).not.toContain(CLIENT_SECRET);
    expect(allLogged).not.toContain("tok-secretcheck");
  });

  it("throws without echoing the request body/secret when the IdP errors", async () => {
    // The error response body deliberately contains the secret to prove the
    // thrown message does NOT propagate it.
    fetchMock.mockResolvedValueOnce(
      new Response(`{"error":"invalid_client","leaked":"${CLIENT_SECRET}"}`, { status: 401 })
    );
    const { getServiceToken } = await import("../bff");

    await expect(getServiceToken()).rejects.toThrow(/HTTP 401/);
    try {
      await getServiceToken();
      expect.unreachable("should have thrown");
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      expect(msg).not.toContain(CLIENT_SECRET);
    }
  });

  it("throws a clear error when AUTH_KEYCLOAK_ISSUER is unset", async () => {
    delete process.env.AUTH_KEYCLOAK_ISSUER;
    const { getServiceToken } = await import("../bff");
    await expect(getServiceToken()).rejects.toThrow(/AUTH_KEYCLOAK_ISSUER/);
  });
});
