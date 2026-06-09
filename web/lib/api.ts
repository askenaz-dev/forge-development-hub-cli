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

// --- Stage-2 admin reads: typed store-unavailable result (capability
// hub-usage-telemetry, tasks 5.3/5.4) ----------------------------------------
//
// The Stage-2 analytics/observability/feedback/activity reads target an OPTIONAL
// telemetry store. When that store is degraded the Go API does NOT 500 — it
// returns HTTP 503 with `{"error":"store_unavailable"}` and a `Retry-After`
// header (see `internal/portalapi/observability.go` storeUnavailable). The web
// panels must surface a RETRY state, not an error panel, in that case.
//
// So these helpers DO NOT throw on a degraded store: they return a discriminated
// union (`ServiceResult<T>`). A genuine transport/auth/BFF failure still throws
// `ApiError` (the panel catches it → ErrorPanel); only the typed degraded-store
// signal is folded into the success-or-retry union the panel branches on.

/** The store_unavailable error code the Go API returns on a degraded store. */
export const STORE_UNAVAILABLE = "store_unavailable";

/**
 * Discriminated result for a Stage-2 admin read. `ok:true` carries the parsed
 * data; `ok:false` means the telemetry store is temporarily unavailable
 * (HTTP 503 `store_unavailable`) and the panel should render its retry state.
 * Any OTHER failure (network, 401/403, malformed) is thrown as `ApiError` and
 * is NOT represented here — callers wrap in try/catch for those.
 */
export type ServiceResult<T> =
  | { ok: true; data: T }
  | { ok: false; storeUnavailable: true; retryAfter?: number };

/** Parse a numeric `Retry-After` (delta-seconds) header; undefined if absent. */
function parseRetryAfter(res: Response): number | undefined {
  const raw = res.headers.get("Retry-After");
  if (!raw) return undefined;
  const n = Number.parseInt(raw, 10);
  return Number.isFinite(n) ? n : undefined;
}

/**
 * GET a Stage-2 admin JSON resource, folding the typed `store_unavailable`
 * (HTTP 503) into a `ServiceResult` instead of throwing. Every other non-2xx
 * status throws `ApiError` so the caller's try/catch surfaces a hard failure.
 */
async function getServiceResult<T>(
  path: string,
  opts: ServiceFetchOptions
): Promise<ServiceResult<T>> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { Authorization: `Bearer ${opts.serviceToken}` },
    signal: opts.signal,
    cache: "no-store",
  });
  if (res.status === 503) {
    // Confirm it is the typed store_unavailable code (a generic 503 from an
    // upstream proxy should still surface as a hard error, not a silent retry).
    const body = await safeText(res);
    if (body.includes(STORE_UNAVAILABLE)) {
      return { ok: false, storeUnavailable: true, retryAfter: parseRetryAfter(res) };
    }
    throw new ApiError(res.status, body);
  }
  if (!res.ok) {
    throw new ApiError(res.status, await safeText(res));
  }
  return { ok: true, data: (await res.json()) as T };
}

/**
 * One onboarding activation event read from the durable telemetry store
 * (`GET /api/v1/admin/activation`, persisted as event=activation — the legacy
 * in-memory ring buffer was removed in `hub-usage-telemetry`, so events now
 * survive an API restart). The record is PII-free: no identity field is carried.
 * Field names match the Go `ActivationEvent` JSON tags.
 */
export interface ActivationEvent {
  time: string;
  event: string;
  step: string;
  wizard_session_id: string;
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

// ============================================================================
// Stage-2 admin telemetry surfaces (capability hub-usage-telemetry)
// ----------------------------------------------------------------------------
// All SERVER-ONLY, service-token authenticated (Bearer = getServiceToken()),
// cached `no-store`. The READS return `ServiceResult<T>` so a degraded store
// (HTTP 503 store_unavailable) surfaces as a retry state rather than throwing;
// the WRITE (claim) returns a plain shape and throws ApiError on hard failure.
// Field names mirror the Go handler JSON exactly (analytics_handlers.go,
// observability.go, activity_handlers.go). Aggregates only — no identity join,
// except the explicit, user-initiated install claim (design D5).
// ============================================================================

/** The closed event set the analytics summary breaks counts down by. */
export type TelemetryEvent =
  | "install"
  | "download"
  | "resolve"
  | "activation"
  | "feedback";

/**
 * `GET /api/v1/admin/analytics/summary` — total retained-event count, the
 * per-event-type breakdown (every bucket always present, zero when absent), and
 * the earliest retained timestamp (`since`, RFC3339 or "" when empty).
 */
export interface AnalyticsSummary {
  total: number;
  by_event: Record<TelemetryEvent, number>;
  since: string;
}

export async function getAnalyticsSummary(
  opts: { serviceToken: string }
): Promise<ServiceResult<AnalyticsSummary>> {
  return getServiceResult<AnalyticsSummary>(
    "/api/v1/admin/analytics/summary",
    opts
  );
}

/** One aggregate row in the top-installed / top-downloaded list. No identity. */
export interface TopComponent {
  kind: string;
  namespace: string;
  name: string;
  count: number;
}

export interface AnalyticsTop {
  metric: "install" | "download";
  items: TopComponent[];
}

/**
 * `GET /api/v1/admin/analytics/top?metric=install|download&limit=N` — the
 * most-counted components for the chosen metric, aggregate-only.
 */
export async function getAnalyticsTop(
  metric: "install" | "download",
  limit: number,
  opts: { serviceToken: string }
): Promise<ServiceResult<AnalyticsTop>> {
  const q = new URLSearchParams({ metric, limit: String(limit) });
  return getServiceResult<AnalyticsTop>(
    `/api/v1/admin/analytics/top?${q.toString()}`,
    opts
  );
}

/** One (date, count) point in an install/event trend. `date` is YYYY-MM-DD. */
export interface TrendPoint {
  date: string;
  count: number;
}

export interface AnalyticsTrends {
  event: string;
  points: TrendPoint[];
}

/**
 * `GET /api/v1/admin/analytics/trends?event=E&days=N` — per-day counts for the
 * event over the window, oldest first.
 */
export async function getAnalyticsTrends(
  event: TelemetryEvent,
  days: number,
  opts: { serviceToken: string }
): Promise<ServiceResult<AnalyticsTrends>> {
  const q = new URLSearchParams({ event, days: String(days) });
  return getServiceResult<AnalyticsTrends>(
    `/api/v1/admin/analytics/trends?${q.toString()}`,
    opts
  );
}

/** One onboarding-funnel step (derived from activation aggregates). */
export interface FunnelStep {
  step: string;
  count: number;
}

export interface AnalyticsFunnel {
  steps: FunnelStep[];
}

/** `GET /api/v1/admin/analytics/funnel` — onboarding funnel, highest first. */
export async function getFunnel(
  opts: { serviceToken: string }
): Promise<ServiceResult<AnalyticsFunnel>> {
  return getServiceResult<AnalyticsFunnel>(
    "/api/v1/admin/analytics/funnel",
    opts
  );
}

/**
 * `GET /api/v1/admin/observability` — first-party site/component health
 * (design D7). Renders entirely from first-party data; the store block reports
 * `available:false`/`event_count:0` when the store is degraded WITHOUT failing
 * the read, so this endpoint never returns store_unavailable.
 */
export interface Observability {
  uptime_seconds: number;
  requests_total: number;
  error_rate: number;
  latency_ms: { p50: number; p95: number };
  store: { available: boolean; event_count: number };
  components: { kind: string; name: string; scan_status: string }[];
  /** Present only when the optional PROMETHEUS_QUERY_URL enrichment is set. */
  prometheus_query_url?: string;
}

export async function getObservability(
  opts: { serviceToken: string }
): Promise<ServiceResult<Observability>> {
  // Observability always renders from first-party data; it is modeled as a
  // ServiceResult for symmetry, but in practice only ok:true / ApiError occur.
  return getServiceResult<Observability>("/api/v1/admin/observability", opts);
}

/** One persisted feedback row. Carries NO identity (design D4/D8). */
export interface FeedbackItem {
  rating: number;
  category: string;
  text: string;
  ts: string;
}

export interface FeedbackPage {
  items: FeedbackItem[];
  count: number;
}

/**
 * `GET /api/v1/admin/feedback?limit=&offset=` — persisted feedback (newest
 * first), paginated, plus the total count. Renders without any LLM.
 */
export async function getFeedback(
  params: { limit: number; offset: number },
  opts: { serviceToken: string }
): Promise<ServiceResult<FeedbackPage>> {
  const q = new URLSearchParams({
    limit: String(params.limit),
    offset: String(params.offset),
  });
  return getServiceResult<FeedbackPage>(
    `/api/v1/admin/feedback?${q.toString()}`,
    opts
  );
}

/**
 * `GET /api/v1/admin/feedback/summary` — the OPTIONAL, feature-flagged
 * LLM-synthesized digest (design D8). `enabled:false` when the flag is off or
 * no provider is configured (no LLM dependency exercised). This endpoint does
 * not touch the store, so it never returns store_unavailable.
 */
export interface FeedbackSummary {
  enabled: boolean;
  summary: string;
  generated_at: string;
}

export async function getFeedbackSummary(
  opts: { serviceToken: string }
): Promise<ServiceResult<FeedbackSummary>> {
  return getServiceResult<FeedbackSummary>(
    "/api/v1/admin/feedback/summary",
    opts
  );
}

/** One voluntarily-claimed install in a user's profile activity feed. */
export interface ClaimedInstall {
  kind: string;
  name: string;
  version: string;
  ts: string;
}

/**
 * `GET /api/v1/admin/activity?user=<email>` — the installs the user voluntarily
 * claimed (design D5), newest first. Empty for an unknown/unclaimed user — never
 * derived by reversing a pseudonymous install_id.
 */
export async function getActivity(
  email: string,
  opts: { serviceToken: string }
): Promise<ServiceResult<{ installs: ClaimedInstall[] }>> {
  const q = new URLSearchParams({ user: email });
  return getServiceResult<{ installs: ClaimedInstall[] }>(
    `/api/v1/admin/activity?${q.toString()}`,
    opts
  );
}

/** Result of a voluntary install claim. `claimed:true` on a recorded 202. */
export type ClaimResult =
  | { ok: true }
  | { ok: false; storeUnavailable: true; retryAfter?: number };

/**
 * `POST /api/v1/admin/activity/claim` — the ONE explicit identity↔telemetry
 * link (design D5 / task 12.2). The web passes the SESSION user's OWN email as
 * `user` and the install-id the user copied from `fdh telemetry claim`. Returns
 * 202 on success. A degraded store yields a store_unavailable retry signal; any
 * other non-2xx (bad request, 403, transport) throws ApiError.
 */
export async function claimInstall(
  installId: string,
  email: string,
  opts: { serviceToken: string }
): Promise<ClaimResult> {
  const res = await fetch(`${BASE}/api/v1/admin/activity/claim`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${opts.serviceToken}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ install_id: installId, user: email }),
    cache: "no-store",
  });
  if (res.status === 503) {
    const body = await safeText(res);
    if (body.includes(STORE_UNAVAILABLE)) {
      return { ok: false, storeUnavailable: true, retryAfter: parseRetryAfter(res) };
    }
    throw new ApiError(res.status, body);
  }
  if (!res.ok) {
    throw new ApiError(res.status, await safeText(res));
  }
  return { ok: true };
}
