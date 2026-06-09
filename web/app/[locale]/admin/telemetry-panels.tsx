import { getTranslations } from "next-intl/server";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { getServiceToken } from "@/lib/bff";
import {
  getAnalyticsSummary,
  getAnalyticsTop,
  getAnalyticsTrends,
  getFunnel,
  getObservability,
  getFeedback,
  getFeedbackSummary,
  type AnalyticsSummary,
  type AnalyticsTop,
  type AnalyticsTrends,
  type AnalyticsFunnel,
  type Observability,
  type FeedbackPage,
  type FeedbackSummary,
  type ServiceResult,
  type TelemetryEvent,
} from "@/lib/api";

/**
 * Stage-2 admin telemetry panels (capability hub-usage-telemetry, tasks 5.2,
 * 6.2, 7.2). Each is a SERVER component that mints the BFF service token and
 * calls the admin-gated API via `web/lib/api.ts`. Three render states per panel:
 *
 *   1. data           — the aggregate view (counts / bars / tables)
 *   2. store retry     — the typed `store_unavailable` (HTTP 503) → a calm
 *                        "retry shortly" notice, NOT an error (the store is an
 *                        optional dependency; this is expected, recoverable)
 *   3. hard error      — a BFF/API/transport failure → ErrorPanel, the catalog
 *                        and the rest of the console are unaffected
 *
 * The page already coarse-gates on `fdh-admins` before rendering these; the Go
 * API independently enforces `admin` on every call (the web check is advisory).
 * No heavy charting dependency: trends/funnel/top render as inline CSS bars and
 * accessible tables.
 */

// --- Shared primitives -------------------------------------------------------

function Th({ children }: { children: React.ReactNode }) {
  return <th className="px-2 py-2 font-medium">{children}</th>;
}

function Td({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return <td className={`px-2 py-2 align-top ${className ?? ""}`}>{children}</td>;
}

function ErrorPanel({ title, body }: { title: string; body: string }) {
  return (
    <div
      role="alert"
      className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
    >
      <p className="font-medium">{title}</p>
      <p className="mt-1 text-xs">{body}</p>
    </div>
  );
}

/**
 * The store-unavailable retry notice. Rendered when a Stage-2 read returns the
 * typed `store_unavailable` (HTTP 503 + Retry-After). It is deliberately NOT an
 * error: the telemetry store is optional, an outage is recoverable, and the
 * catalog/console keep working. Carries the server-suggested retry hint when one
 * is present.
 */
function StoreRetryNotice({
  message,
  retryAfter,
}: {
  message: string;
  retryAfter?: number;
}) {
  return (
    <div
      role="status"
      className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs font-medium text-amber-700 dark:text-amber-400"
    >
      {message}
      {retryAfter ? ` (~${retryAfter}s)` : ""}
    </div>
  );
}

/** A single big stat with a label, matching the Phase-1 admin Stat styling. */
function Stat({ label, value }: { label: string; value: number | string }) {
  return (
    <div className="rounded-md border bg-card p-3">
      <div className="text-2xl font-bold tabular-nums">{value}</div>
      <div className="text-xs uppercase tracking-wide text-muted-foreground">
        {label}
      </div>
    </div>
  );
}

/**
 * A horizontal bar row: a label, a count, and a proportional CSS bar. No chart
 * library — a `width: %` div keyed off the row's share of the max. `max` of 0
 * renders empty bars (avoids divide-by-zero).
 */
function Bar({
  label,
  count,
  max,
  title,
}: {
  label: React.ReactNode;
  count: number;
  max: number;
  title?: string;
}) {
  const pct = max > 0 ? Math.round((count / max) * 100) : 0;
  return (
    <div className="flex items-center gap-3 text-sm" title={title}>
      <div className="w-40 shrink-0 truncate font-mono text-xs">{label}</div>
      <div
        className="h-3 flex-1 overflow-hidden rounded-sm bg-muted"
        role="presentation"
      >
        <div
          className="h-full rounded-sm bg-primary/70"
          style={{ width: `${pct}%` }}
        />
      </div>
      <div className="w-12 shrink-0 text-right tabular-nums">{count}</div>
    </div>
  );
}

/**
 * Render-state helper: given a `ServiceResult` (or an error captured during the
 * fetch), pick exactly one of {error, store-retry, data}. Keeps every panel's
 * branching identical and exhaustive.
 */
type PanelState<T> =
  | { kind: "data"; data: T }
  | { kind: "store"; retryAfter?: number }
  | { kind: "error"; detail: string };

async function load<T>(
  fn: (opts: { serviceToken: string }) => Promise<ServiceResult<T>>
): Promise<PanelState<T>> {
  try {
    const serviceToken = await getServiceToken();
    const res = await fn({ serviceToken });
    if (res.ok) return { kind: "data", data: res.data };
    return { kind: "store", retryAfter: res.retryAfter };
  } catch (err) {
    return { kind: "error", detail: err instanceof Error ? err.message : "unknown error" };
  }
}

// --- Analytics panel ---------------------------------------------------------

const EVENT_ORDER: TelemetryEvent[] = [
  "install",
  "download",
  "resolve",
  "activation",
  "feedback",
];

export async function AnalyticsPanel() {
  const t = await getTranslations("adminTelemetry");

  // Five independent reads; each degrades on its own. We mint one token per call
  // (cached in bff.ts), so this is cheap.
  const [summary, top, downloads, trends, funnel] = await Promise.all([
    load<AnalyticsSummary>((o) => getAnalyticsSummary(o)),
    load<AnalyticsTop>((o) => getAnalyticsTop("install", 10, o)),
    load<AnalyticsTop>((o) => getAnalyticsTop("download", 10, o)),
    load<AnalyticsTrends>((o) => getAnalyticsTrends("install", 30, o)),
    load<AnalyticsFunnel>((o) => getFunnel(o)),
  ]);

  return (
    <section className="mt-8">
      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t("analyticsTitle")}</CardTitle>
          <CardDescription>{t("analyticsDescription")}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-8">
          {/* Summary counts */}
          <div>
            <h3 className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
              {t("summaryHeading")}
            </h3>
            {summary.kind === "error" ? (
              <ErrorPanel
                title={t("errorTitle")}
                body={t("errorBody", { detail: summary.detail })}
              />
            ) : summary.kind === "store" ? (
              <StoreRetryNotice
                message={t("storeUnavailable")}
                retryAfter={summary.retryAfter}
              />
            ) : (
              <>
                <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-6">
                  <Stat label={t("statTotalEvents")} value={summary.data.total} />
                  {EVENT_ORDER.map((e) => (
                    <Stat
                      key={e}
                      label={t(`event_${e}` as Parameters<typeof t>[0])}
                      value={summary.data.by_event[e] ?? 0}
                    />
                  ))}
                </div>
                <p className="mt-2 text-xs text-muted-foreground">
                  {summary.data.since
                    ? t("since", { since: summary.data.since })
                    : t("sinceEmpty")}
                </p>
              </>
            )}
          </div>

          {/* Top installed + top downloaded */}
          <div className="grid gap-6 md:grid-cols-2">
            <TopList
              heading={t("topInstalledHeading")}
              empty={t("topEmpty")}
              state={top}
              labels={{
                error: t("errorTitle"),
                errorBody: (d: string) => t("errorBody", { detail: d }),
                store: t("storeUnavailable"),
              }}
            />
            <TopList
              heading={t("topDownloadedHeading")}
              empty={t("topEmpty")}
              state={downloads}
              labels={{
                error: t("errorTitle"),
                errorBody: (d: string) => t("errorBody", { detail: d }),
                store: t("storeUnavailable"),
              }}
            />
          </div>

          {/* Install trend */}
          <div>
            <h3 className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
              {t("trendHeading")}
            </h3>
            {trends.kind === "error" ? (
              <ErrorPanel
                title={t("errorTitle")}
                body={t("errorBody", { detail: trends.detail })}
              />
            ) : trends.kind === "store" ? (
              <StoreRetryNotice
                message={t("storeUnavailable")}
                retryAfter={trends.retryAfter}
              />
            ) : trends.data.points.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t("trendEmpty")}</p>
            ) : (
              <div className="space-y-1.5">
                {(() => {
                  const max = Math.max(
                    1,
                    ...trends.data.points.map((p) => p.count)
                  );
                  return trends.data.points.map((p) => (
                    <Bar key={p.date} label={p.date} count={p.count} max={max} />
                  ));
                })()}
              </div>
            )}
          </div>

          {/* Onboarding funnel */}
          <div>
            <h3 className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
              {t("funnelHeading")}
            </h3>
            {funnel.kind === "error" ? (
              <ErrorPanel
                title={t("errorTitle")}
                body={t("errorBody", { detail: funnel.detail })}
              />
            ) : funnel.kind === "store" ? (
              <StoreRetryNotice
                message={t("storeUnavailable")}
                retryAfter={funnel.retryAfter}
              />
            ) : funnel.data.steps.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t("funnelEmpty")}</p>
            ) : (
              <div className="space-y-1.5">
                {(() => {
                  const max = Math.max(
                    1,
                    ...funnel.data.steps.map((s) => s.count)
                  );
                  return funnel.data.steps.map((s) => (
                    <Bar key={s.step} label={s.step} count={s.count} max={max} />
                  ));
                })()}
              </div>
            )}
          </div>
        </CardContent>
      </Card>
    </section>
  );
}

function TopList({
  heading,
  empty,
  state,
  labels,
}: {
  heading: string;
  empty: string;
  state: PanelState<AnalyticsTop>;
  labels: { error: string; errorBody: (d: string) => string; store: string };
}) {
  return (
    <div>
      <h3 className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {heading}
      </h3>
      {state.kind === "error" ? (
        <ErrorPanel title={labels.error} body={labels.errorBody(state.detail)} />
      ) : state.kind === "store" ? (
        <StoreRetryNotice message={labels.store} retryAfter={state.retryAfter} />
      ) : state.data.items.length === 0 ? (
        <p className="text-sm text-muted-foreground">{empty}</p>
      ) : (
        <div className="space-y-1.5">
          {(() => {
            const max = Math.max(1, ...state.data.items.map((i) => i.count));
            return state.data.items.map((i) => (
              <Bar
                key={`${i.kind}/${i.namespace}/${i.name}`}
                label={`${i.kind}/${i.name}`}
                title={`${i.kind}/${i.namespace}/${i.name}`}
                count={i.count}
                max={max}
              />
            ));
          })()}
        </div>
      )}
    </div>
  );
}

// --- Observability panel -----------------------------------------------------

function formatUptime(seconds: number): string {
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const parts: string[] = [];
  if (d) parts.push(`${d}d`);
  if (h || d) parts.push(`${h}h`);
  parts.push(`${m}m`);
  return parts.join(" ");
}

export async function ObservabilityPanel() {
  const t = await getTranslations("adminTelemetry");
  const state = await load<Observability>((o) => getObservability(o));

  // Aggregate component scan health for a compact summary line.
  let healthSummary: { pass: number; warn: number; fail: number; none: number } | null =
    null;
  if (state.kind === "data") {
    const summary = { pass: 0, warn: 0, fail: 0, none: 0 };
    for (const c of state.data.components) {
      if (c.scan_status === "pass") summary.pass += 1;
      else if (c.scan_status === "warn") summary.warn += 1;
      else if (c.scan_status === "fail") summary.fail += 1;
      else summary.none += 1;
    }
    healthSummary = summary;
  }

  return (
    <section className="mt-8">
      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t("obsTitle")}</CardTitle>
          <CardDescription>{t("obsDescription")}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-6">
          {state.kind === "error" ? (
            <ErrorPanel
              title={t("errorTitle")}
              body={t("errorBody", { detail: state.detail })}
            />
          ) : state.kind === "store" ? (
            <StoreRetryNotice
              message={t("storeUnavailable")}
              retryAfter={state.retryAfter}
            />
          ) : (
            <>
              <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
                <Stat
                  label={t("obsUptime")}
                  value={formatUptime(state.data.uptime_seconds)}
                />
                <Stat
                  label={t("obsRequests")}
                  value={state.data.requests_total}
                />
                <Stat
                  label={t("obsErrorRate")}
                  value={`${(state.data.error_rate * 100).toFixed(2)}%`}
                />
                <Stat
                  label={t("obsP50")}
                  value={`${state.data.latency_ms.p50}ms`}
                />
                <Stat
                  label={t("obsP95")}
                  value={`${state.data.latency_ms.p95}ms`}
                />
              </div>

              {/* Store health */}
              <div className="flex flex-wrap items-center gap-3 text-sm">
                <span className="text-xs uppercase tracking-wide text-muted-foreground">
                  {t("obsStoreHealth")}
                </span>
                <span
                  className={`inline-flex items-center gap-2 rounded-md px-2 py-0.5 font-mono text-xs font-medium ${
                    state.data.store.available
                      ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-500"
                      : "bg-amber-500/10 text-amber-700 dark:text-amber-400"
                  }`}
                >
                  {state.data.store.available
                    ? t("obsStoreUp")
                    : t("obsStoreDown")}
                </span>
                <span className="text-xs text-muted-foreground">
                  {t("obsEventCount", { count: state.data.store.event_count })}
                </span>
              </div>

              {/* Component health summary + table */}
              <div>
                <h3 className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                  {t("obsComponentHealth")}
                </h3>
                {state.data.components.length === 0 ? (
                  <p className="text-sm text-muted-foreground">
                    {t("obsComponentsEmpty")}
                  </p>
                ) : (
                  <>
                    {healthSummary ? (
                      <p className="mb-2 text-xs text-muted-foreground">
                        {t("obsHealthSummary", {
                          pass: healthSummary.pass,
                          warn: healthSummary.warn,
                          fail: healthSummary.fail,
                          none: healthSummary.none,
                        })}
                      </p>
                    ) : null}
                    <div className="max-h-72 overflow-auto rounded-md border">
                      <table className="w-full border-collapse text-sm">
                        <thead className="sticky top-0 bg-card">
                          <tr className="border-b text-left text-xs uppercase tracking-wide text-muted-foreground">
                            <Th>{t("colKind")}</Th>
                            <Th>{t("colName")}</Th>
                            <Th>{t("colScan")}</Th>
                          </tr>
                        </thead>
                        <tbody>
                          {state.data.components.map((c) => (
                            <tr
                              key={`${c.kind}/${c.name}`}
                              className="border-b last:border-0"
                            >
                              <Td className="font-mono">{c.kind}</Td>
                              <Td className="font-medium">{c.name}</Td>
                              <Td className="font-mono text-xs">
                                {c.scan_status}
                              </Td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </>
                )}
              </div>
            </>
          )}
        </CardContent>
      </Card>
    </section>
  );
}

// --- Feedback panel ----------------------------------------------------------

const FEEDBACK_PAGE_SIZE = 50;

export async function FeedbackPanel() {
  const t = await getTranslations("adminTelemetry");

  const [list, summary] = await Promise.all([
    load<FeedbackPage>((o) => getFeedback({ limit: FEEDBACK_PAGE_SIZE, offset: 0 }, o)),
    load<FeedbackSummary>((o) => getFeedbackSummary(o)),
  ]);

  return (
    <section className="mt-8">
      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t("feedbackTitle")}</CardTitle>
          <CardDescription>{t("feedbackDescription")}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-6">
          {/* Optional LLM digest (renders only when enabled; otherwise hidden,
              and the raw list below always renders without any LLM). */}
          {summary.kind === "data" && summary.data.enabled ? (
            <div className="rounded-md border bg-muted/40 px-3 py-2">
              <h3 className="mb-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                {t("feedbackSummaryHeading")}
              </h3>
              {summary.data.summary ? (
                <p className="text-sm">{summary.data.summary}</p>
              ) : (
                <p className="text-sm text-muted-foreground">
                  {t("feedbackSummaryPending")}
                </p>
              )}
              {summary.data.generated_at ? (
                <p className="mt-1 text-[10px] text-muted-foreground">
                  {t("feedbackSummaryGenerated", {
                    at: summary.data.generated_at,
                  })}
                </p>
              ) : null}
            </div>
          ) : null}

          {/* Raw paginated list — always rendered regardless of the summary. */}
          {list.kind === "error" ? (
            <ErrorPanel
              title={t("errorTitle")}
              body={t("errorBody", { detail: list.detail })}
            />
          ) : list.kind === "store" ? (
            <StoreRetryNotice
              message={t("storeUnavailable")}
              retryAfter={list.retryAfter}
            />
          ) : list.data.items.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t("feedbackEmpty")}</p>
          ) : (
            <div className="overflow-x-auto">
              <p className="mb-2 text-xs text-muted-foreground">
                {t("feedbackCount", { count: list.data.count })}
              </p>
              <table className="w-full border-collapse text-sm">
                <thead>
                  <tr className="border-b text-left text-xs uppercase tracking-wide text-muted-foreground">
                    <Th>{t("colRating")}</Th>
                    <Th>{t("colCategory")}</Th>
                    <Th>{t("colText")}</Th>
                    <Th>{t("colTime")}</Th>
                  </tr>
                </thead>
                <tbody>
                  {list.data.items.map((f, i) => (
                    <tr key={`${f.ts}-${i}`} className="border-b last:border-0">
                      <Td className="font-mono tabular-nums">
                        {f.rating > 0 ? f.rating : "—"}
                      </Td>
                      <Td className="font-mono text-xs">{f.category || "—"}</Td>
                      <Td className="max-w-md whitespace-pre-wrap break-words">
                        {f.text || "—"}
                      </Td>
                      <Td className="font-mono text-xs text-muted-foreground">
                        {f.ts}
                      </Td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
    </section>
  );
}
