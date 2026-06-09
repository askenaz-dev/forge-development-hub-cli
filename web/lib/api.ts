/**
 * Typed client for the FDH portal API.
 *
 * This is a hand-written facade. Future work (M5/M6 stretch) replaces it
 * with a generated client from the OpenAPI spec at
 * `../internal/portalapi/openapi.yaml` via `openapi-typescript`.
 *
 * Every function is server-friendly: it accepts an optional `fetch`
 * implementation so server components can pass through `next: { revalidate }`
 * caching hints if desired. Authenticated endpoints carry a bearer token
 * via the `token` parameter; anonymous reads omit it.
 */

const BASE = process.env.FDH_API_BASE_URL ?? "http://localhost:8080";

export interface SkillSummary {
  namespace: string;
  name: string;
  description?: string;
  owner_team?: string;
  tags?: string[];
  latest_version: string;
  latest_hash: string;
  scan_status: "pass" | "warn" | "fail" | "none";
}

export interface SkillVersion {
  version: string;
  content_hash: string;
  published_at: string;
  published_by?: string;
  changelog_url?: string;
  scan_status: "pass" | "warn" | "fail" | "none";
  signature?: string;
  skill_md_url: string;
}

export interface SkillManifest {
  namespace: string;
  name: string;
  description: string;
  owner_team?: string;
  tags?: string[];
  latest: string;
  versions: SkillVersion[];
}

export interface UserIdentity {
  role: "anonymous" | "consumer" | "author" | "reviewer" | "publisher" | "admin";
  sub?: string;
  name?: string;
  email?: string;
  claims?: string[];
}

export interface SkillsPage {
  items: SkillSummary[];
  next_cursor: string | null;
}

interface FetchOptions {
  token?: string;
  signal?: AbortSignal;
  // next-cache hints; ignored in browser
  revalidate?: number;
}

async function getJSON<T>(path: string, opts?: FetchOptions): Promise<T> {
  const headers: HeadersInit = {};
  if (opts?.token) headers["Authorization"] = `Bearer ${opts.token}`;

  const res = await fetch(`${BASE}${path}`, {
    headers,
    signal: opts?.signal,
    next: opts?.revalidate !== undefined ? { revalidate: opts.revalidate } : undefined,
  });
  if (!res.ok) {
    throw new ApiError(res.status, await safeText(res));
  }
  return (await res.json()) as T;
}

async function getText(path: string, opts?: FetchOptions): Promise<string> {
  const headers: HeadersInit = {};
  if (opts?.token) headers["Authorization"] = `Bearer ${opts.token}`;
  const res = await fetch(`${BASE}${path}`, {
    headers,
    signal: opts?.signal,
    next: opts?.revalidate !== undefined ? { revalidate: opts.revalidate } : undefined,
  });
  if (!res.ok) {
    throw new ApiError(res.status, await safeText(res));
  }
  return await res.text();
}

async function safeText(res: Response): Promise<string> {
  try {
    return await res.text();
  } catch {
    return "";
  }
}

export class ApiError extends Error {
  constructor(public status: number, public body: string) {
    super(`api error ${status}: ${body}`);
  }
}

// --- Endpoints ---

export interface ListSkillsParams {
  q?: string;
  namespace?: string;
  tag?: string;
  scan_status?: string;
  limit?: number;
  cursor?: string;
}

export async function listSkills(
  params: ListSkillsParams = {},
  opts?: FetchOptions
): Promise<SkillsPage> {
  const q = new URLSearchParams();
  if (params.q) q.set("q", params.q);
  if (params.namespace) q.set("namespace", params.namespace);
  if (params.tag) q.set("tag", params.tag);
  if (params.scan_status) q.set("scan_status", params.scan_status);
  if (params.limit) q.set("limit", String(params.limit));
  if (params.cursor) q.set("cursor", params.cursor);
  const query = q.toString();
  return getJSON<SkillsPage>(`/api/v1/skills${query ? `?${query}` : ""}`, opts);
}

export async function getSkill(
  namespace: string,
  name: string,
  opts?: FetchOptions
): Promise<SkillManifest> {
  return getJSON<SkillManifest>(`/api/v1/skills/${namespace}/${name}`, opts);
}

export async function getSkillVersion(
  namespace: string,
  name: string,
  version: string,
  opts?: FetchOptions
): Promise<SkillVersion> {
  return getJSON<SkillVersion>(
    `/api/v1/skills/${namespace}/${name}/versions/${version}`,
    opts
  );
}

export async function getSkillMarkdown(
  namespace: string,
  name: string,
  version: string,
  opts?: FetchOptions
): Promise<string> {
  return getText(
    `/api/v1/skills/${namespace}/${name}/versions/${version}/skill-md`,
    opts
  );
}

export async function getCurrentUser(opts?: FetchOptions): Promise<UserIdentity> {
  return getJSON<UserIdentity>("/api/v1/auth/me", opts);
}

// --- Components (kind-aware catalog) ---
//
// The hub publishes four primitive kinds. `/api/v1/components` is the
// kind-aware catalog; `/api/v1/skills` (above) is its kind=skill view,
// retained for backward compatibility.

export type Kind = "skill" | "rule" | "agent" | "hook";

export interface ComponentSummary {
  kind: Kind;
  namespace: string;
  name: string;
  description?: string;
  owner_team?: string;
  tags?: string[];
  latest_version: string;
  latest_hash: string;
  scan_status: "pass" | "warn" | "fail" | "none";
}

export interface ComponentVersion {
  version: string;
  content_hash: string;
  published_at: string;
  published_by?: string;
  scan_status: "pass" | "warn" | "fail" | "none";
  signature?: string;
  document_url: string;
}

export interface ComponentManifest {
  kind: Kind;
  namespace: string;
  name: string;
  description: string;
  owner_team?: string;
  tags?: string[];
  latest: string;
  versions: ComponentVersion[];
}

export interface ComponentsPage {
  items: ComponentSummary[];
  next_cursor: string | null;
}

export interface ListComponentsParams {
  kind?: Kind;
  q?: string;
  namespace?: string;
  tag?: string;
  scan_status?: string;
  limit?: number;
  cursor?: string;
}

export async function listComponents(
  params: ListComponentsParams = {},
  opts?: FetchOptions
): Promise<ComponentsPage> {
  const q = new URLSearchParams();
  if (params.kind) q.set("kind", params.kind);
  if (params.q) q.set("q", params.q);
  if (params.namespace) q.set("namespace", params.namespace);
  if (params.tag) q.set("tag", params.tag);
  if (params.scan_status) q.set("scan_status", params.scan_status);
  if (params.limit) q.set("limit", String(params.limit));
  if (params.cursor) q.set("cursor", params.cursor);
  const query = q.toString();
  return getJSON<ComponentsPage>(`/api/v1/components${query ? `?${query}` : ""}`, opts);
}

export async function getComponent(
  kind: Kind,
  namespace: string,
  name: string,
  opts?: FetchOptions
): Promise<ComponentManifest> {
  return getJSON<ComponentManifest>(`/api/v1/components/${kind}/${namespace}/${name}`, opts);
}

export async function getComponentVersion(
  kind: Kind,
  namespace: string,
  name: string,
  version: string,
  opts?: FetchOptions
): Promise<ComponentVersion> {
  return getJSON<ComponentVersion>(
    `/api/v1/components/${kind}/${namespace}/${name}/versions/${version}`,
    opts
  );
}

export async function getComponentDocument(
  kind: Kind,
  namespace: string,
  name: string,
  version: string,
  opts?: FetchOptions
): Promise<string> {
  return getText(
    `/api/v1/components/${kind}/${namespace}/${name}/versions/${version}/document`,
    opts
  );
}

// --- Admin / BFF endpoints (SERVER-ONLY, service-token authenticated) ---
//
// These call admin-gated API paths with a Keycloak client-credentials service
// token minted by `web/lib/bff.ts` (NOT a user bearer — the user's IdP token is
// not in the session; see auth.ts). The web's role check is advisory UX-gating;
// the Go API independently enforces the role on every call (403 otherwise).
// Responses are never cached (`cache: "no-store"`): admin reads must be fresh
// and the bearer is a secret.

interface ServiceFetchOptions {
  /** Keycloak client-credentials token from `getServiceToken()` (bff.ts). */
  serviceToken: string;
  signal?: AbortSignal;
}

/** GET a JSON resource with a service-token bearer and no caching. */
async function getJSONService<T>(path: string, opts: ServiceFetchOptions): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { Authorization: `Bearer ${opts.serviceToken}` },
    signal: opts.signal,
    cache: "no-store",
  });
  if (!res.ok) {
    throw new ApiError(res.status, await safeText(res));
  }
  return (await res.json()) as T;
}

/** POST (no body) to a JSON endpoint with a service-token bearer, no caching. */
async function postJSONService<T>(path: string, opts: ServiceFetchOptions): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    method: "POST",
    headers: { Authorization: `Bearer ${opts.serviceToken}` },
    signal: opts.signal,
    cache: "no-store",
  });
  if (!res.ok) {
    throw new ApiError(res.status, await safeText(res));
  }
  return (await res.json()) as T;
}

/**
 * One onboarding activation event from the in-memory ring buffer
 * (`GET /api/v1/admin/activation`). The buffer is EPHEMERAL — cleared on API
 * restart; durable analytics arrive in `hub-usage-telemetry` (Phase 2).
 * Field names match the Go `ActivationEvent` JSON tags.
 */
export interface ActivationEvent {
  time: string;
  event: string;
  step: string;
  wizard_session_id: string;
  user_id?: string;
  locale?: string;
  os?: string;
}

/**
 * getActivation reads the activation ring buffer. Admin-only on the API side
 * (403 unless the service token maps to `admin`). Returns the events in
 * chronological order plus a count; an empty ring yields `{events: [], count: 0}`.
 */
export async function getActivation(
  opts: { serviceToken: string }
): Promise<{ events: ActivationEvent[]; count: number }> {
  return getJSONService<{ events: ActivationEvent[]; count: number }>(
    "/api/v1/admin/activation",
    opts
  );
}

/**
 * triggerRefresh forces an immediate registry refresh (`POST /api/v1/refresh`).
 * Gated server-side at `publisher` minimum (admins satisfy it). Returns the
 * refreshed snapshot's timestamp and component/skill counts.
 */
export async function triggerRefresh(
  opts: { serviceToken: string }
): Promise<{ refreshed_at: string; component_count: number; skill_count: number }> {
  return postJSONService<{
    refreshed_at: string;
    component_count: number;
    skill_count: number;
  }>("/api/v1/refresh", opts);
}

/**
 * One catalog component the signed-in user authored, derived from
 * `forge-development-hub` Git authorship matched by email
 * (`GET /api/v1/admin/contributions?email=...`). This is an internal BFF
 * surface (NOT in openapi.yaml). The list is read-only and an email-match
 * heuristic; an empty list (not an error) means no commits matched.
 */
export interface Contribution {
  kind: string;
  name: string;
  commit_count: number;
  last_commit: string;
}

/**
 * getContributions returns the components Git-authored by `email`. Admin-gated
 * on the API side exactly like getActivation. The web calls this ONLY for the
 * logged-in user's own email — never an arbitrary email. An empty/unmatched
 * email yields `{email, contributions: []}` (no error).
 */
export async function getContributions(
  email: string,
  opts: { serviceToken: string }
): Promise<{ email: string; contributions: Contribution[] }> {
  const q = new URLSearchParams({ email });
  return getJSONService<{ email: string; contributions: Contribution[] }>(
    `/api/v1/admin/contributions?${q.toString()}`,
    opts
  );
}

// --- Catalog statistics (DERIVED client-side; no /api/v1/stats — Decision D2) ---

/**
 * Aggregate catalog statistics, computed purely from the served component list
 * (Decision D2: no separate `/api/v1/stats` endpoint; openapi.yaml unchanged).
 *
 * `deprecated` and `yanked` are always 0 in Phase 1: the catalog does not yet
 * emit a lifecycle `status` field (`component-lifecycle` is additive but
 * unimplemented — Decision D3), so the admin surface labels them "lifecycle
 * status not yet tracked" rather than asserting them as verified.
 */
export interface CatalogStats {
  total: number;
  totalVersions: number;
  perKind: Record<string, number>;
  deprecated: number;
  yanked: number;
}

/**
 * aggregateCatalogStats computes per-kind/total/version counts from a list of
 * components (the `ComponentSummary[]` `listComponents()` returns). PURE — no
 * I/O, no mutation of the input.
 *
 * `totalVersions` counts one version per component: the served summary carries
 * only `latest_version` (the list endpoint does not expand `versions[]`), so a
 * present `latest_version` contributes exactly one to the version total. When
 * the catalog later exposes full version histories the helper can sum them.
 */
export function aggregateCatalogStats(components: ComponentSummary[]): CatalogStats {
  const perKind: Record<string, number> = { skill: 0, rule: 0, agent: 0, hook: 0 };
  let totalVersions = 0;
  for (const c of components) {
    perKind[c.kind] = (perKind[c.kind] ?? 0) + 1;
    if (c.latest_version) {
      totalVersions += 1;
    }
  }
  return {
    total: components.length,
    totalVersions,
    perKind,
    // Lifecycle status is not yet tracked (Decision D3): no component version
    // carries a `status` field, so these are honestly 0 and labeled as such.
    deprecated: 0,
    yanked: 0,
  };
}
