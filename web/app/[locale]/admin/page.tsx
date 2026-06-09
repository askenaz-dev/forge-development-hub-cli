import { redirect } from "next/navigation";
import { getTranslations, setRequestLocale } from "next-intl/server";
import { auth } from "@/auth";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { ScanStatusBadge } from "@/components/scan-status-badge";
import {
  listComponents,
  aggregateCatalogStats,
  getActivation,
  type ComponentSummary,
  type ActivationEvent,
} from "@/lib/api";
import { getServiceToken } from "@/lib/bff";
import { resolvePortalRole, hasMinRole } from "@/lib/roles";
import { RefreshControl } from "./refresh-control";
import {
  AnalyticsPanel,
  ObservabilityPanel,
  FeedbackPanel,
} from "./telemetry-panels";
import {
  ImportPanel,
  HarnessPanel,
  CuratePanel,
  type CatalogRef,
} from "./gitops-panels";

/**
 * /admin — the portal operations console. Auth-gated, then coarse-gated to the
 * `fdh-admins` group for what it RENDERS (advisory UX-gating). Every privileged
 * call below is ALSO enforced server-side by the Go API against a validated
 * service token (403 otherwise) — the web check is not the security boundary.
 *
 * Data sources, all already-existing (Phase 1 adds no persistence):
 *   - Catalog stats + the component-health table are DERIVED client-side from
 *     `GET /api/v1/components` (Decision D2 — no `/api/v1/stats` endpoint).
 *   - The activation log is the ephemeral in-memory ring read via the BFF
 *     service token; it is labeled ephemeral and never implies durability.
 *   - The registry-refresh control posts through a Server Action.
 */

/**
 * The ephemeral-activation notice. This is a Phase-1 spec requirement (task 6.1,
 * Decision in design.md): the activation surface MUST always carry this literal
 * so it never implies durability the platform does not yet have. It is a fixed
 * string (not only translated copy) so an automated check can assert its
 * presence regardless of locale.
 */
const ACTIVATION_EPHEMERAL_LABEL =
  "Ephemeral — in-memory, cleared on restart; durable analytics arrive in Phase 2 (hub-usage-telemetry)";

export default async function AdminPage({
  params,
}: {
  params: Promise<{ locale: string }>;
}) {
  const { locale } = await params;
  setRequestLocale(locale);

  const session = await auth();
  if (!session) redirect("/auth/signin?redirect_to=/admin");

  const t = await getTranslations("admin");

  const groups: string[] = session.user?.groups ?? [];
  // Resolve the viewer's portal role (advisory UX-gating). Every privileged call
  // below is ALSO enforced server-side by the Go API against a validated service
  // token; a non-admin reaching here triggers NO admin-only call.
  const role = resolvePortalRole(groups);
  const isAdmin = hasMinRole(role, "admin");
  // The Phase-3 GitOps write surface is reachable per-role: import is author+,
  // harness edit is publisher+, curate is admin. The Phase-1/2 ops console
  // (catalog stats, activation, refresh, telemetry) stays admin-only.
  const canContribute = hasMinRole(role, "author");

  // A consumer (or unmapped) viewer sees neither surface.
  if (!isAdmin && !canContribute) {
    return (
      <div className="container py-12">
        <h1 className="text-3xl font-bold tracking-tight">{t("gatedTitle")}</h1>
        <p className="mt-2 text-muted-foreground">
          {t.rich("gatedMessage", {
            group: () => <code>fdh-admins</code>,
          })}
        </p>
      </div>
    );
  }

  // An author/publisher who is NOT an admin sees ONLY the GitOps contribution
  // surface — never the admin-only ops console (which would 403 on every call).
  if (!isAdmin) {
    const catalog = await listComponents({ limit: 200 }, { revalidate: 30 }).catch(
      () => ({ items: [] as ComponentSummary[], next_cursor: null })
    );
    const catalogRefs: CatalogRef[] = catalog.items.map((c) => ({
      kind: c.kind,
      name: c.name,
    }));
    return (
      <div className="container py-12">
        <h1 className="text-3xl font-bold tracking-tight">{t("contributeTitle")}</h1>
        <p className="mt-2 max-w-3xl text-muted-foreground">
          {t("contributeIntro")}
        </p>
        <GitopsSurface
          role={role}
          catalog={catalogRefs}
          t={t}
        />
      </div>
    );
  }

  // --- Catalog reads (anonymous endpoint; safe even without the service token).
  // A high limit so the stats/health table cover the whole catalog (today: low
  // tens of components — well under the 200 page cap noted in Decision D2).
  const catalog = await listComponents({ limit: 200 }, { revalidate: 30 }).catch(
    () => ({ items: [] as ComponentSummary[], next_cursor: null })
  );
  const components = catalog.items;
  const stats = aggregateCatalogStats(components);

  // --- Privileged read: activation log via the BFF service token. Wrapped so a
  // BFF/API failure renders a clear error panel instead of a 500.
  let activation: { events: ActivationEvent[]; count: number } | null = null;
  let activationError: string | null = null;
  try {
    const serviceToken = await getServiceToken();
    activation = await getActivation({ serviceToken });
  } catch (err) {
    activationError = err instanceof Error ? err.message : "unknown error";
  }

  const kindOrder = ["skill", "rule", "agent", "hook"];

  return (
    <div className="container py-12">
      <h1 className="text-3xl font-bold tracking-tight">{t("title")}</h1>
      <p className="mt-2 max-w-3xl text-muted-foreground">{t("intro")}</p>

      {/* --- 3.2 Catalog statistics ------------------------------------- */}
      <section className="mt-8">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">{t("statsTitle")}</CardTitle>
            <CardDescription>
              {t("statsDescription", { components: stats.total })}
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-6">
            <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
              <Stat label={t("statTotal")} value={stats.total} />
              <Stat label={t("statVersions")} value={stats.totalVersions} />
              <Stat
                label={t("statDeprecated")}
                value={stats.deprecated}
                note={t("lifecycleNotTracked")}
              />
              <Stat
                label={t("statYanked")}
                value={stats.yanked}
                note={t("lifecycleNotTracked")}
              />
            </div>

            <div>
              <h3 className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                {t("perKindHeading")}
              </h3>
              <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
                {kindOrder.map((k) => (
                  <Stat key={k} label={k} value={stats.perKind[k] ?? 0} />
                ))}
              </div>
            </div>
          </CardContent>
        </Card>
      </section>

      {/* --- 3.3 Component-health table --------------------------------- */}
      <section className="mt-8">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">{t("healthTitle")}</CardTitle>
            <CardDescription>{t("healthDescription")}</CardDescription>
          </CardHeader>
          <CardContent>
            {components.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t("healthEmpty")}</p>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full border-collapse text-sm">
                  <thead>
                    <tr className="border-b text-left text-xs uppercase tracking-wide text-muted-foreground">
                      <Th>{t("colKind")}</Th>
                      <Th>{t("colNamespace")}</Th>
                      <Th>{t("colName")}</Th>
                      <Th>{t("colVersion")}</Th>
                      <Th>{t("colScan")}</Th>
                      <Th>
                        {t("colLifecycle")}{" "}
                        <span
                          className="cursor-help text-muted-foreground"
                          title={t("lifecycleProvisional")}
                        >
                          *
                        </span>
                      </Th>
                    </tr>
                  </thead>
                  <tbody>
                    {components.map((c) => (
                      <tr
                        key={`${c.kind}/${c.namespace}/${c.name}`}
                        className="border-b last:border-0"
                      >
                        <Td className="font-mono">{c.kind}</Td>
                        <Td className="font-mono text-muted-foreground">
                          {c.namespace}
                        </Td>
                        <Td className="font-medium">{c.name}</Td>
                        <Td className="font-mono">v{c.latest_version}</Td>
                        <Td>
                          <ScanStatusBadge status={c.scan_status} />
                        </Td>
                        <Td>
                          <span
                            className="font-mono text-muted-foreground"
                            title={t("lifecycleProvisional")}
                          >
                            {t("lifecycleActive")}
                          </span>
                        </Td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                <p className="mt-3 text-xs text-muted-foreground">
                  * {t("lifecycleProvisional")}
                </p>
              </div>
            )}
          </CardContent>
        </Card>
      </section>

      {/* --- 3.4 Activation log + 6.1 ephemeral label ------------------- */}
      <section className="mt-8">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">{t("activationTitle")}</CardTitle>
            <CardDescription>{t("activationDescription")}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {/* The ephemeral notice ALWAYS renders, independent of fetch
                outcome (task 6.1). It is a fixed literal string. */}
            <div
              role="note"
              className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs font-medium text-amber-700 dark:text-amber-400"
            >
              {ACTIVATION_EPHEMERAL_LABEL}
            </div>

            {activationError !== null ? (
              <ErrorPanel
                title={t("errorPanelTitle")}
                body={t("errorPanelBody", { detail: activationError })}
              />
            ) : activation === null || activation.count === 0 ? (
              <p className="text-sm text-muted-foreground">
                {t("activationEmpty")}
              </p>
            ) : (
              <div className="overflow-x-auto">
                <p className="mb-2 text-xs text-muted-foreground">
                  {t("activationCount", { count: activation.count })}
                </p>
                <table className="w-full border-collapse text-sm">
                  <thead>
                    <tr className="border-b text-left text-xs uppercase tracking-wide text-muted-foreground">
                      <Th>{t("colTime")}</Th>
                      <Th>{t("colEvent")}</Th>
                      <Th>{t("colStep")}</Th>
                      <Th>{t("colSession")}</Th>
                      <Th>{t("colLocale")}</Th>
                      <Th>{t("colOs")}</Th>
                    </tr>
                  </thead>
                  <tbody>
                    {activation.events.map((e, i) => (
                      <tr
                        key={`${e.wizard_session_id}-${e.time}-${i}`}
                        className="border-b last:border-0"
                      >
                        <Td className="font-mono text-xs">{e.time}</Td>
                        <Td className="font-mono">{e.event}</Td>
                        <Td>{e.step}</Td>
                        <Td className="font-mono text-xs text-muted-foreground">
                          {e.wizard_session_id}
                        </Td>
                        <Td className="font-mono text-xs">{e.locale ?? "—"}</Td>
                        <Td className="font-mono text-xs">{e.os ?? "—"}</Td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </CardContent>
        </Card>
      </section>

      {/* --- 3.5 Registry-refresh control ------------------------------- */}
      <section className="mt-8">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">{t("refreshTitle")}</CardTitle>
            <CardDescription>{t("refreshDescription")}</CardDescription>
          </CardHeader>
          <CardContent>
            <RefreshControl
              labels={{
                button: t("refreshButton"),
                pending: t("refreshPending"),
                successTemplate: t("refreshSuccess", {
                  at: "{at}",
                  components: "{components}",
                  skills: "{skills}",
                }),
                failureTemplate: t("refreshFailure", { detail: "{detail}" }),
              }}
            />
          </CardContent>
        </Card>
      </section>

      {/* --- Stage-2 telemetry surfaces (capability hub-usage-telemetry) ---
          Durable analytics, first-party observability, and the feedback
          channel. Each panel mints the BFF service token, calls the admin-gated
          API, and degrades independently: a typed `store_unavailable` shows a
          calm retry notice (the store is optional), a BFF/API failure shows an
          error panel — neither affects the Phase-1 content above. */}
      <AnalyticsPanel />
      <ObservabilityPanel />
      <FeedbackPanel />

      {/* --- Phase-3 GitOps write surface (capability portal-gitops-write) ---
          Role-gated CONFIG-via-PR panels. Each posts to a same-origin BFF route
          that re-checks the role and forwards to the Go API with the service
          credential; the bot opens but never merges. The admin sees all three
          panels (import/harness/curate); the panels are also gated by `role`. */}
      <GitopsSurface
        role={role}
        catalog={components.map((c) => ({ kind: c.kind, name: c.name }))}
        t={t}
      />
    </div>
  );
}

/**
 * GitopsSurface renders the role-appropriate subset of the three Phase-3 write
 * panels, gated by the viewer's resolved `role` (advisory UX-gating; the BFF and
 * Go API re-enforce the minimum role on every call). Import is author+, harness
 * is publisher+, curate is admin. Labels are resolved here (server-side) from the
 * `admin` namespace and handed to the client islands as plain strings.
 */
function GitopsSurface({
  role,
  catalog,
  t,
}: {
  role: ReturnType<typeof resolvePortalRole>;
  catalog: CatalogRef[];
  // The translator returned by getTranslations("admin").
  t: Awaited<ReturnType<typeof getTranslations<"admin">>>;
}) {
  const common = {
    proposeNotice: t("gitopsProposeNotice"),
    submit: t("gitopsSubmit"),
    pending: t("gitopsPending"),
    prOpenedTemplate: t("gitopsPrOpened", { url: "{url}" }),
    alreadyOpenTemplate: t("gitopsAlreadyOpen", { url: "{url}" }),
    notConfigured: t("gitopsNotConfigured"),
    forbidden: t("gitopsForbidden"),
    failureTemplate: t("gitopsFailure", { detail: "{detail}" }),
    viewPr: t("gitopsViewPr"),
  };

  return (
    <section className="mt-12 space-y-6">
      <div>
        <h2 className="text-2xl font-bold tracking-tight">
          {t("gitopsSectionTitle")}
        </h2>
        <p className="mt-1 max-w-3xl text-sm text-muted-foreground">
          {t("gitopsSectionIntro")}
        </p>
      </div>

      {hasMinRole(role, "author") && (
        <ImportPanel
          common={common}
          labels={{
            title: t("importTitle"),
            description: t("importDescription"),
            nameLabel: t("importNameLabel"),
            namePlaceholder: t("importNamePlaceholder"),
            descLabel: t("importDescLabel"),
            descPlaceholder: t("importDescPlaceholder"),
            ownerTeamLabel: t("importOwnerTeamLabel"),
            ownerTeamPlaceholder: t("importOwnerTeamPlaceholder"),
            agentsLabel: t("importAgentsLabel"),
            agentsHint: t("importAgentsHint"),
            zipLabel: t("importZipLabel"),
            zipHint: t("importZipHint"),
            modeForm: t("importModeForm"),
            modeZip: t("importModeZip"),
            nameRequired: t("importNameRequired"),
            zipRequired: t("importZipRequired"),
          }}
        />
      )}

      {hasMinRole(role, "publisher") && (
        <HarnessPanel
          common={common}
          catalog={catalog}
          labels={{
            title: t("harnessTitle"),
            description: t("harnessDescription"),
            harnessLabel: t("harnessNameLabel"),
            harnessPlaceholder: t("harnessNamePlaceholder"),
            ownerTeamLabel: t("harnessOwnerTeamLabel"),
            descLabel: t("harnessDescLabel"),
            addHeading: t("harnessAdd"),
            removeHeading: t("harnessRemove"),
            emptyCatalog: t("harnessEmptyCatalog"),
            noChanges: t("harnessNoChanges"),
            unknownComponentTemplate: t("harnessUnknownComponent", {
              kind: "{kind}",
              name: "{name}",
            }),
          }}
        />
      )}

      {hasMinRole(role, "admin") && (
        <CuratePanel
          common={common}
          catalog={catalog}
          labels={{
            title: t("curateTitle"),
            description: t("curateDescription"),
            componentLabel: t("curateComponentLabel"),
            componentPlaceholder: t("curateComponentPlaceholder"),
            actionLabel: t("curateActionLabel"),
            actionSetDefaultTrue: t("curateSetDefaultTrue"),
            actionSetDefaultFalse: t("curateSetDefaultFalse"),
            actionDeprecate: t("curateDeprecate"),
            actionYank: t("curateYank"),
            versionLabel: t("curateVersionLabel"),
            versionPlaceholder: t("curateVersionPlaceholder"),
            versionRequired: t("curateVersionRequired"),
            noUnyankNote: t("curateNoUnyank"),
            componentRequired: t("curateComponentRequired"),
          }}
        />
      )}
    </section>
  );
}

function Stat({
  label,
  value,
  note,
}: {
  label: string;
  value: number;
  note?: string;
}) {
  return (
    <div className="rounded-md border bg-card p-3">
      <div className="text-2xl font-bold tabular-nums">{value}</div>
      <div className="text-xs uppercase tracking-wide text-muted-foreground">
        {label}
      </div>
      {note ? (
        <div className="mt-1 text-[10px] leading-tight text-muted-foreground">
          {note}
        </div>
      ) : null}
    </div>
  );
}

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
