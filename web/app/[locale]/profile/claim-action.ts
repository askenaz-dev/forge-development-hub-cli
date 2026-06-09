"use server";

import { revalidatePath } from "next/cache";
import { auth } from "@/auth";
import { getServiceToken } from "@/lib/bff";
import { claimInstall } from "@/lib/api";

/**
 * Server Action backing the profile install-claim control (capability
 * hub-usage-telemetry, design D5, task 8.2).
 *
 * This is the ONE explicit identity↔telemetry link in the whole platform, and
 * it happens ONLY here, ONLY because the signed-in user pasted their machine's
 * install-id (the code `fdh telemetry claim` prints). The flow runs ENTIRELY on
 * the server:
 *
 *   1. Resolve the live session — the claim is bound to the SESSION user's own
 *      email (the stable profile key, consistent with the contributions
 *      derivation). The web NEVER lets a user claim an install into someone
 *      else's profile: `user` is taken from the session, never from form input.
 *   2. Mint the Keycloak client-credentials service token (server-only bff.ts)
 *      and POST it to the admin-gated `/api/v1/admin/activity/claim`. The API
 *      enforces `admin` independently; the BFF service principal earns it.
 *   3. Revalidate the profile path so the freshly-claimed install appears in the
 *      activity feed on the next render.
 *
 * The install-id is pseudonymous and is NEVER reversed: this records a forward
 * (install_id → email) link the user volunteered; it does not de-anonymize the
 * events table (design D4/D5, task 12.2).
 */
export interface ClaimState {
  status: "idle" | "success" | "store_unavailable" | "error";
  /** A short, non-sensitive failure detail for the error state. */
  detail?: string;
}

export async function claimInstallAction(
  _prev: ClaimState,
  formData: FormData
): Promise<ClaimState> {
  const session = await auth();
  const email = session?.user?.email;
  if (!session || !email) {
    return { status: "error", detail: "not signed in" };
  }

  const installId = String(formData.get("install_id") ?? "").trim();
  if (!installId) {
    return { status: "error", detail: "empty install id" };
  }

  try {
    const serviceToken = await getServiceToken();
    // `user` is the SESSION email — never form-supplied (privacy invariant).
    const res = await claimInstall(installId, email, { serviceToken });
    if (!res.ok) {
      return { status: "store_unavailable" };
    }
    // Surface the new install in the feed on the next render.
    revalidatePath("/[locale]/profile", "page");
    return { status: "success" };
  } catch (err) {
    const detail = err instanceof Error ? err.message : "unknown error";
    return { status: "error", detail };
  }
}
