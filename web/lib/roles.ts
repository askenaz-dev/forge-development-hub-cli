/**
 * Portal role resolution from Keycloak group memberships.
 *
 * This mirrors — intentionally, on the web side — the Go API's role-map logic
 * (`internal/portalapi/auth/auth.go`): a user's groups (the `groups` claim) are
 * looked up against a group→role map, and the HIGHEST-precedence mapped role
 * wins over the ladder `anonymous < consumer < author < reviewer < publisher
 * < admin`. An authenticated user whose groups map to nothing is `consumer`
 * (the API's "authenticated-but-unmapped default").
 *
 * TRUST MODEL: this resolution is ADVISORY. It decides what the web renders
 * (e.g. whether to show the `/admin` console). It is NOT a security boundary.
 * The Go API independently validates the bearer token against the IdP JWKS and
 * enforces the required role on every privileged call (403 otherwise). A forged
 * client cannot bypass the API gate by lying about its groups here.
 *
 * The default group→role map below mirrors the deployed `role-map.yaml`
 * (`deploy/askenaz/role-map.yaml`, `compose/api/role-map.yaml`). It is kept in
 * sync by convention; the API's copy remains authoritative.
 */

export type PortalRole =
  | "anonymous"
  | "consumer"
  | "author"
  | "reviewer"
  | "publisher"
  | "admin";

/**
 * Numeric precedence of each role (higher = more privileged), matching the
 * Go API's `RoleRank`. Unknown roles rank as `anonymous` (0).
 */
const ROLE_RANK: Record<PortalRole, number> = {
  anonymous: 0,
  consumer: 1,
  author: 2,
  reviewer: 3,
  publisher: 4,
  admin: 5,
};

/**
 * Default Keycloak-group → portal-role map. Mirrors the deployed
 * `role-map.yaml` consumed by the Go API. The human admin group is
 * `fdh-admins`; the dedicated BFF service-account group (`fdh-portal-svc`)
 * is intentionally absent here because it never appears in a human session's
 * groups — it is carried only by the client-credentials service token the API
 * validates server-side.
 */
const GROUP_ROLE_MAP: Record<string, PortalRole> = {
  "fdh-admins": "admin",
  "fdh-publishers": "publisher",
  "fdh-reviewers": "reviewer",
  "fdh-authors": "author",
};

/** rankOf returns a role's precedence, defaulting unknown roles to anonymous. */
function rankOf(role: PortalRole): number {
  return ROLE_RANK[role] ?? 0;
}

/**
 * resolvePortalRole maps an AUTHENTICATED user's Keycloak groups to their
 * portal role. It is called only from a live session (`session.user.groups`),
 * so the baseline is `consumer` — the Go API's "authenticated-but-unmapped"
 * default — NOT `anonymous`. Per the spec, an authenticated user with no
 * mapped group is shown as `consumer`.
 *
 * Rules (matching the Go API's `Validate` role-precedence loop):
 *   - Authenticated, no group maps to a role  → "consumer" (the baseline).
 *   - One or more groups map                  → highest-precedence mapped role.
 *
 * Pure and side-effect free. `groups` may be undefined/empty (a session with
 * no group claims), which still resolves to `consumer`.
 */
export function resolvePortalRole(groups: string[] | undefined | null): PortalRole {
  // Baseline for any authenticated principal. Climb to the highest-precedence
  // mapped role over the user's groups (an empty list keeps the baseline).
  let role: PortalRole = "consumer";
  for (const g of groups ?? []) {
    const mapped = GROUP_ROLE_MAP[g];
    if (mapped && rankOf(mapped) > rankOf(role)) {
      role = mapped;
    }
  }
  return role;
}

/** hasMinRole reports whether `actual` satisfies the minimum `required` role. */
export function hasMinRole(actual: PortalRole, required: PortalRole): boolean {
  return rankOf(actual) >= rankOf(required);
}
