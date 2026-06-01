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

// --- Telemetry (events) ---
//
// The browser posts product events to the same-origin BFF route `/api/events`,
// which forwards to the portal's `/api/v1/events`. The frontend never targets
// the portal or any analytics backend directly.

export interface TelemetryEvent {
  event_name: string;
  attributes?: Record<string, string>;
}

/** postEvent fires a product event from the browser. Best-effort and silent:
 *  telemetry failures never affect the page. */
export async function postEvent(event: TelemetryEvent): Promise<void> {
  try {
    await fetch("/api/events", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ schema_version: 1, ...event }),
      keepalive: true,
    });
  } catch {
    // swallow — telemetry is not load-bearing
  }
}

// --- Admin insights ---

export interface KV {
  key: string;
  count: number;
}

export interface InsightsSummary {
  window_start: string;
  window_end: string;
  total: number;
  event_counts: Record<string, number>;
  top_downloads: KV[];
  demand_gaps: KV[];
  top_not_found: KV[];
  top_installs: KV[];
  top_uninstalls: KV[];
  install_failures_by_class: Record<string, number>;
  feedback: Record<string, number>;
}

/** getInsights reads the aggregated admin telemetry view. Server-side only:
 *  the portal enforces the `admin` role against the forwarded bearer token. */
export async function getInsights(opts?: FetchOptions): Promise<InsightsSummary> {
  return getJSON<InsightsSummary>("/api/v1/admin/insights", opts);
}
