import "server-only";

import { NextResponse } from "next/server";
import { auth } from "@/auth";
import { getServiceToken } from "@/lib/bff";
import { resolvePortalRole, hasMinRole, type PortalRole } from "@/lib/roles";
import type { GitopsResult, Requestor } from "@/lib/api";

/**
 * Shared server-only plumbing for the three Phase-3 GitOps BFF forwarder routes
 * (capability portal-gitops-write).
 *
 * Each route (`/api/gitops/import|harness|curate`) is a thin same-origin shim
 * the browser POSTs to. The shim:
 *   1. Reads the NextAuth session and resolves the portal role from
 *      `session.user.groups` (`resolvePortalRole`).
 *   2. Enforces the action's MINIMUM role BEFORE forwarding — author for import,
 *      publisher for harness, admin for curate. A caller below the minimum gets a
 *      403 and the request is NEVER forwarded (no service token is minted, no
 *      bot is invoked). This is the ADVISORY UX gate (design D8); the Go API
 *      re-enforces the SAME minimum via `HasMinRole` (authoritative).
 *   3. Mints the Phase-1 BFF SERVICE CREDENTIAL (`getServiceToken()`, server-only
 *      bff.ts) and calls the Go endpoint with it as the Bearer — NEVER a user
 *      IdP bearer (the web strips it from the session cookie; see auth.ts).
 *   4. Passes the server-verified user identity (session name/email) along as
 *      TRUSTED METADATA for PR attribution. The credited identity comes from the
 *      verified session, not client free-text.
 *
 * The service token / client secret are minted and used ENTIRELY on the server
 * here; they never reach the browser. `import "server-only"` makes that a
 * build-time guarantee.
 */

/** Minimum portal role for each GitOps action (design D8). */
export const GITOPS_MIN_ROLE = {
  import: "author",
  harness: "publisher",
  curate: "admin",
} as const satisfies Record<string, PortalRole>;

export type GitopsAction = keyof typeof GITOPS_MIN_ROLE;

/**
 * The result of the BFF pre-forward gate. On `ok` the caller has the minted
 * service token and the trusted requestor metadata to forward; otherwise a ready
 * `NextResponse` (401 unauthenticated / 403 under-role) to return verbatim.
 */
type GateOutcome =
  | { ok: true; serviceToken: string; requestor: Requestor; role: PortalRole }
  | { ok: false; response: NextResponse };

/**
 * gateAndAuthorize performs the advisory role gate and mints the service token.
 * It NEVER mints a token for an unauthenticated or under-role caller — the
 * forward is short-circuited with a JSON error matching the Go envelope shape
 * (`{error, message}`) so the client handles 401/403 uniformly across surfaces.
 */
export async function gateAndAuthorize(action: GitopsAction): Promise<GateOutcome> {
  const minRole = GITOPS_MIN_ROLE[action];

  const session = await auth();
  if (!session) {
    return {
      ok: false,
      response: NextResponse.json(
        { error: "unauthenticated", message: "sign in required" },
        { status: 401 }
      ),
    };
  }

  const role = resolvePortalRole(session.user?.groups);
  if (!hasMinRole(role, minRole)) {
    // Advisory gate: refuse BEFORE forwarding. No service token is minted, no
    // bot is invoked, no branch/PR is created (spec: "the BFF returns a forbidden
    // response and does not forward").
    return {
      ok: false,
      response: NextResponse.json(
        {
          error: "forbidden",
          message: `role '${minRole}' or above required`,
        },
        { status: 403 }
      ),
    };
  }

  let serviceToken: string;
  try {
    serviceToken = await getServiceToken();
  } catch (err) {
    // A BFF/IdP failure minting the service token is a transport failure, not a
    // gitops outcome; surface it as 502 (never the token/secret).
    return {
      ok: false,
      response: NextResponse.json(
        { error: "bff_token_failed", message: String(err) },
        { status: 502 }
      ),
    };
  }

  const requestor: Requestor = {
    name: session.user?.name ?? session.user?.preferredUsername ?? null,
    email: session.user?.email ?? null,
    // Forward the server-verified role: the Go API uses it as the AUTHORITATIVE
    // per-user gate (the service credential always maps to admin, so this is the
    // signal that actually differentiates author/publisher/admin) and credits it
    // in the PR body. Trusted because it is resolved here from the session, never
    // client input.
    role,
  };

  return { ok: true, serviceToken, requestor, role };
}

/**
 * Translate a `GitopsResult` (the typed union the lib/api callers return) into
 * the HTTP response the browser island consumes. The status codes mirror the Go
 * API so the client's branching is identical whether it reads this shim's
 * response or (hypothetically) the API directly:
 *   - ok            → 201 { pr_url, branch, already_open:false }
 *   - alreadyOpen   → 200 { pr_url, branch, already_open:true }   (idempotency)
 *   - gitopsDisabled→ 503 { error:"gitops_not_configured" }       (calm notice)
 *   - forbidden     → 403 { error:"forbidden" }
 *   - validation    → 422 { error:<code>, message }               (failing check)
 */
export function gitopsResultResponse(result: GitopsResult): NextResponse {
  if (result.ok) {
    return NextResponse.json(
      { pr_url: result.url, branch: result.branch, already_open: false, merged: false },
      { status: 201 }
    );
  }
  if ("alreadyOpen" in result) {
    return NextResponse.json(
      { pr_url: result.url, branch: result.branch, already_open: true, merged: false },
      { status: 200 }
    );
  }
  if ("gitopsDisabled" in result) {
    const res = NextResponse.json(
      {
        error: "gitops_not_configured",
        message: "the portal GitHub App is not configured yet",
      },
      { status: 503 }
    );
    if (result.retryAfter) res.headers.set("Retry-After", String(result.retryAfter));
    return res;
  }
  if ("forbidden" in result) {
    return NextResponse.json(
      { error: "forbidden", message: result.message },
      { status: 403 }
    );
  }
  // validation (bundle/scan/portability, name collision, lifecycle, unknown component)
  return NextResponse.json(
    { error: result.code || "validation_failed", message: result.message },
    { status: 422 }
  );
}

/** Wrap an unexpected thrown error (ApiError/transport) as a 502 forward failure. */
export function forwardFailed(err: unknown): NextResponse {
  return NextResponse.json(
    { error: "forward_failed", message: err instanceof Error ? err.message : String(err) },
    { status: 502 }
  );
}
