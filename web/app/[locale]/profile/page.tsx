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
import { resolvePortalRole } from "@/lib/roles";
import { resolveAvatar } from "@/lib/avatar";
import { getServiceToken } from "@/lib/bff";
import {
  getContributions,
  getActivity,
  type Contribution,
  type ClaimedInstall,
} from "@/lib/api";
import { ClaimControl } from "./claim-control";

/**
 * /profile — auth-gated identity + recognition surface (portal-admin-surface
 * tasks 4.1–4.3).
 *
 * Server component. It shows:
 *   - 4.1 Identity (name/email/username) + the resolved portal role, derived
 *     from `session.user.groups` via `resolvePortalRole()` (the precedence
 *     ladder). This web-side resolution is ADVISORY UX only; the Go API
 *     enforces roles independently on every privileged call.
 *   - 4.2 A deterministic avatar from the user's email (Gravatar by default,
 *     `PORTAL_AVATAR_PROVIDER=local` for an offline initials SVG). No upload.
 *   - 4.3 "Your contributions" — components the user Git-authored in
 *     forge-development-hub, matched by email. DERIVED + read-only, an
 *     email-match heuristic; an empty match is a labeled empty state, never an
 *     error. We request ONLY the logged-in user's own email and wrap the call
 *     in try/catch so a BFF/API hiccup degrades gracefully (no 500).
 *
 * Unauthenticated visitors are bounced to sign-in.
 */
export default async function ProfilePage({
  params,
}: {
  params: Promise<{ locale: string }>;
}) {
  const { locale } = await params;
  setRequestLocale(locale);

  const session = await auth();
  if (!session) {
    redirect("/auth/signin?redirect_to=/profile");
  }

  const t = await getTranslations("profile");

  const name = session.user?.name ?? undefined;
  const email = session.user?.email ?? undefined;
  const preferredUsername: string | undefined = session.user?.preferredUsername;
  const groups: string[] = session.user?.groups ?? [];

  // 4.1 — resolved portal role (advisory; API enforces independently).
  const role = resolvePortalRole(groups);

  // 4.2 — deterministic avatar. `resolveAvatar` runs server-side (it uses
  // node:crypto for the Gravatar hash) and falls back to a local SVG when the
  // provider is `local` or no email is present.
  const avatar = resolveAvatar(name, email);

  // Activity feed (capability hub-usage-telemetry, design D5, task 8) — a
  // GitHub-style UNIFIED chronological timeline that merges TWO independent
  // sources, each row labeled by source:
  //   - "contribution": components THIS user Git-authored in forge-development-hub,
  //     matched by email (4.3, identity-bound, DERIVED — no telemetry).
  //   - "install": installs this user VOLUNTARILY claimed via `fdh telemetry
  //     claim` (D5). Empty until the user pastes a claim code below; NEVER
  //     derived by reversing the pseudonymous install_id.
  // The two sources fail independently and non-fatally: a contributions hiccup
  // shows a note but the installs still render, and vice-versa. A degraded
  // telemetry store yields a retry banner for the installs half only.
  let contributions: Contribution[] = [];
  let contributionsErrored = false;
  let claimedInstalls: ClaimedInstall[] = [];
  let activityState: "ok" | "store_unavailable" | "error" = "ok";
  try {
    const serviceToken = await getServiceToken();
    // Both reads share the one minted token (bff.ts caches it anyway). We always
    // pass THIS session's own email — never an arbitrary one (privacy invariant).
    const [contribRes, activityRes] = await Promise.allSettled([
      getContributions(email ?? "", { serviceToken }),
      getActivity(email ?? "", { serviceToken }),
    ]);
    // Contributions: a failure degrades to a note but never breaks the page or
    // the installs half.
    if (contribRes.status === "fulfilled") {
      contributions = contribRes.value.contributions;
    } else {
      contributionsErrored = true;
    }
    // Claimed installs: distinguish the typed degraded-store retry (ok:false)
    // from a hard failure (rejected promise).
    if (activityRes.status === "fulfilled") {
      if (activityRes.value.ok) {
        claimedInstalls = activityRes.value.data.installs;
      } else {
        activityState = "store_unavailable";
      }
    } else {
      activityState = "error";
    }
  } catch {
    // Minting the service token itself failed — non-fatal to identity + groups.
    // Degrade BOTH halves; never surface the underlying error (infra detail).
    contributionsErrored = true;
    activityState = "error";
  }

  // Merge both sources into ONE chronological feed, newest first. Each entry
  // carries its `source` so the row can render a Contribution / Install badge.
  // Unparseable timestamps sort last (sortKey 0) but still render.
  const feed: ActivityEntry[] = [
    ...contributions.map<ActivityEntry>((c) => ({
      source: "contribution",
      kind: c.kind,
      name: c.name,
      ts: c.last_commit,
      sortKey: toEpoch(c.last_commit),
      commitCount: c.commit_count,
    })),
    ...claimedInstalls.map<ActivityEntry>((i) => ({
      source: "install",
      kind: i.kind,
      name: i.name,
      ts: i.ts,
      sortKey: toEpoch(i.ts),
      version: i.version,
    })),
  ].sort((a, b) => b.sortKey - a.sortKey);

  return (
    <div className="container py-12">
      <h1 className="text-3xl font-bold tracking-tight">{t("title")}</h1>

      <div className="mt-8 grid gap-4 md:grid-cols-2">
        {/* Identity + avatar + resolved role */}
        <Card>
          <CardHeader>
            <CardTitle className="text-base">{t("identityHeading")}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4 text-sm">
            <div className="flex items-center gap-4">
              {/* eslint-disable-next-line @next/next/no-img-element --
                  a plain <img> avoids next/image's loader/CSP interplay; the
                  src is a Gravatar URL or an inline data: SVG, both small. */}
              <img
                src={avatar.src}
                alt={t("avatarAlt")}
                width={64}
                height={64}
                className="h-16 w-16 shrink-0 rounded-lg border bg-muted object-cover"
              />
              <div className="min-w-0">
                <p className="truncate font-medium">{name ?? "—"}</p>
                <p className="truncate text-xs text-muted-foreground">
                  {email ?? "—"}
                </p>
              </div>
            </div>

            <div className="space-y-2 border-t pt-4">
              <Row label={t("name")} value={name ?? "—"} />
              <Row label={t("email")} value={email ?? "—"} />
              <Row label={t("username")} value={preferredUsername ?? "—"} />
              <div className="flex items-baseline gap-3">
                <span className="w-24 shrink-0 text-xs uppercase tracking-wide text-muted-foreground">
                  {t("portalRole")}
                </span>
                <span className="inline-flex items-center rounded-md bg-primary/10 px-2 py-0.5 font-mono text-xs font-medium text-primary">
                  {role}
                </span>
              </div>
            </div>
            <p className="text-xs text-muted-foreground">{t("roleHint")}</p>
          </CardContent>
        </Card>

        {/* Groups */}
        <Card>
          <CardHeader>
            <CardTitle className="text-base">{t("groupsHeading")}</CardTitle>
            <CardDescription>{t("groupsDescription")}</CardDescription>
          </CardHeader>
          <CardContent>
            {groups.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t("noGroups")}</p>
            ) : (
              <ul className="space-y-1">
                {groups.map((g) => (
                  <li key={g} className="font-mono text-xs">
                    {g}
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>

      {/* Activity — a GitHub-style UNIFIED chronological feed (design D5, task
          8). It merges TWO independent sources, each row labeled by source:
          Git contributions (DERIVED, identity-bound) and voluntarily-claimed
          installs (appear ONLY after the user pastes a `fdh telemetry claim`
          code; the platform never reverses a pseudonymous install_id). The two
          halves degrade independently and non-fatally. */}
      <Card className="mt-4">
        <CardHeader>
          <CardTitle className="text-base">{t("activityHeading")}</CardTitle>
          <CardDescription>{t("activityDescription")}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-5">
          {/* Per-source degradation notes. Each renders inline ABOVE the merged
              feed so a hiccup in one source never hides the other. */}
          {contributionsErrored && (
            <p className="text-sm text-muted-foreground">
              {t("contributionsError")}
            </p>
          )}
          {activityState === "store_unavailable" && (
            <p
              role="status"
              className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs font-medium text-amber-700 dark:text-amber-400"
            >
              {t("activityStoreUnavailable")}
            </p>
          )}
          {activityState === "error" && (
            <p className="text-sm text-muted-foreground">{t("activityError")}</p>
          )}

          {feed.length === 0 ? (
            // Both sources empty (and neither errored, or the notes above already
            // explained the gap): a single, friendly empty state.
            <div className="space-y-1">
              <p className="text-sm text-muted-foreground">
                {contributionsErrored || activityState !== "ok"
                  ? t("activityFeedEmpty")
                  : t("activityEmpty")}
              </p>
              {!contributionsErrored && activityState === "ok" && (
                <p className="text-xs text-muted-foreground">
                  {t("contributionsEmptyHint")}
                </p>
              )}
            </div>
          ) : (
            <ul className="divide-y rounded-md border">
              {feed.map((e, idx) => (
                <li
                  key={`${e.source}/${e.kind}/${e.name}/${e.ts}/${idx}`}
                  className="flex flex-wrap items-center justify-between gap-2 px-4 py-3"
                >
                  <div className="flex items-center gap-3">
                    {/* Source badge — the GitHub-style "what kind of event". */}
                    <span
                      className={
                        e.source === "contribution"
                          ? "inline-flex items-center rounded-md bg-primary/10 px-2 py-0.5 text-xs font-medium text-primary"
                          : "inline-flex items-center rounded-md bg-emerald-500/10 px-2 py-0.5 text-xs font-medium text-emerald-700 dark:text-emerald-400"
                      }
                    >
                      {e.source === "contribution"
                        ? t("activitySourceContribution")
                        : t("activitySourceInstall")}
                    </span>
                    <span className="inline-flex items-center rounded-md bg-muted px-2 py-0.5 font-mono text-xs uppercase text-muted-foreground">
                      {e.kind}
                    </span>
                    <span className="font-mono text-sm">{e.name}</span>
                    {e.source === "install" && e.version ? (
                      <span className="font-mono text-xs text-muted-foreground">
                        v{e.version}
                      </span>
                    ) : null}
                  </div>
                  <div className="flex items-center gap-4 text-xs text-muted-foreground">
                    <span>
                      {e.source === "contribution"
                        ? t("activityContributionDetail", {
                            count: e.commitCount ?? 0,
                          })
                        : t("activityInstallDetail")}
                    </span>
                    <span title={t("contributionsLastCommit")}>
                      {formatCommitDate(e.ts, locale)}
                    </span>
                  </div>
                </li>
              ))}
            </ul>
          )}

          {/* The voluntary claim control. Binds to THIS session's email. */}
          <div className="border-t pt-4">
            <ClaimControl
              labels={{
                inputLabel: t("claimInputLabel"),
                placeholder: t("claimPlaceholder"),
                button: t("claimButton"),
                pending: t("claimPending"),
                success: t("claimSuccess"),
                storeUnavailable: t("claimStoreUnavailable"),
                failureTemplate: t("claimFailure", { detail: "{detail}" }),
                hint: t("claimHint"),
              }}
            />
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

/**
 * One entry in the unified profile activity feed (design D5). It is the merge
 * of two independent sources — Git `contribution`s (identity-bound, DERIVED) and
 * voluntarily-claimed `install`s — flattened to a common shape so they render in
 * one chronological list, each row labeled by `source`. `sortKey` is the epoch
 * ms of `ts` (0 when unparseable) used purely for newest-first ordering.
 */
interface ActivityEntry {
  source: "contribution" | "install";
  kind: string;
  name: string;
  ts: string;
  sortKey: number;
  /** Contributions only: number of commits matched. */
  commitCount?: number;
  /** Installs only: the claimed version (may be empty). */
  version?: string;
}

/** Epoch-ms of an RFC3339 timestamp, or 0 when unparseable (sorts last). */
function toEpoch(value: string): number {
  const t = Date.parse(value);
  return Number.isNaN(t) ? 0 : t;
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline gap-3">
      <span className="w-24 shrink-0 text-xs uppercase tracking-wide text-muted-foreground">
        {label}
      </span>
      <span className="font-mono">{value}</span>
    </div>
  );
}

/**
 * Format an RFC3339 commit timestamp as a locale-aware date. Falls back to the
 * raw string if it is unparseable (we never throw in the render path).
 */
function formatCommitDate(value: string, locale: string): string {
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return new Intl.DateTimeFormat(locale, {
    year: "numeric",
    month: "short",
    day: "numeric",
  }).format(d);
}
