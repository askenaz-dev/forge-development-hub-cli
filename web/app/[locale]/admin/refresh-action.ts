"use server";

import { auth } from "@/auth";
import { getServiceToken } from "@/lib/bff";
import { triggerRefresh } from "@/lib/api";
import { resolvePortalRole, hasMinRole } from "@/lib/roles";

/**
 * Server Action backing the /admin registry-refresh control.
 *
 * The whole flow runs on the server: the Keycloak client-credentials token is
 * minted here (via the SERVER-ONLY `getServiceToken()`), used for the single
 * `POST /api/v1/refresh` call, and never returned to the client. Only the
 * serializable result of the refresh (or a failure message) crosses back to the
 * browser — never the token, never the client secret.
 *
 * Defence in depth: the web re-checks the caller's portal role before minting a
 * token (advisory UX-gating), but the Go API is the authoritative gate — it
 * enforces `publisher`-minimum on `POST /api/v1/refresh` against the validated
 * service token regardless of what the web believes.
 */
export interface RefreshState {
  status: "idle" | "success" | "error";
  refreshedAt?: string;
  componentCount?: number;
  skillCount?: number;
  /** A short, non-sensitive failure detail (HTTP status / message). */
  detail?: string;
}

export async function refreshRegistryAction(
  _prev: RefreshState,
  _formData: FormData
): Promise<RefreshState> {
  // Advisory web-side gate: never mint a service token for a non-admin caller.
  // (The API enforces the role independently on the call itself.)
  const session = await auth();
  const role = resolvePortalRole(session?.user?.groups);
  if (!session || !hasMinRole(role, "admin")) {
    return { status: "error", detail: "forbidden" };
  }

  try {
    const serviceToken = await getServiceToken();
    const res = await triggerRefresh({ serviceToken });
    return {
      status: "success",
      refreshedAt: res.refreshed_at,
      componentCount: res.component_count,
      skillCount: res.skill_count,
    };
  } catch (err) {
    // Surface a terse, non-sensitive detail. Never echo the token/secret; the
    // BFF and API client already avoid putting those in error messages.
    const detail = err instanceof Error ? err.message : "unknown error";
    return { status: "error", detail };
  }
}
