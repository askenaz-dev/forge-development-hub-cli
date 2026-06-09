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
import { getContributions, type Contribution } from "@/lib/api";

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

  // 4.3 — contributions, derived from Git authorship by THIS user's email only.
  // A failure here must not break the page: degrade to an error note.
  let contributions: Contribution[] = [];
  let contributionsErrored = false;
  try {
    const serviceToken = await getServiceToken();
    const res = await getContributions(email ?? "", { serviceToken });
    contributions = res.contributions;
  } catch {
    // Never surface the underlying error (it may carry infra detail); the API
    // gate / BFF mint failing here is non-fatal to identity + groups.
    contributionsErrored = true;
  }

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

      {/* Your contributions (DERIVED, read-only, email-match heuristic) */}
      <Card className="mt-4">
        <CardHeader>
          <CardTitle className="text-base">
            {t("contributionsHeading")}
          </CardTitle>
          <CardDescription>{t("contributionsDescription")}</CardDescription>
        </CardHeader>
        <CardContent>
          {contributionsErrored ? (
            <p className="text-sm text-muted-foreground">
              {t("contributionsError")}
            </p>
          ) : contributions.length === 0 ? (
            <div className="space-y-1">
              <p className="text-sm text-muted-foreground">
                {t("contributionsEmpty", { email: email ?? "—" })}
              </p>
              <p className="text-xs text-muted-foreground">
                {t("contributionsEmptyHint")}
              </p>
            </div>
          ) : (
            <ul className="divide-y rounded-md border">
              {contributions.map((c) => (
                <li
                  key={`${c.kind}/${c.name}`}
                  className="flex flex-wrap items-center justify-between gap-2 px-4 py-3"
                >
                  <div className="flex items-center gap-3">
                    <span className="inline-flex items-center rounded-md bg-muted px-2 py-0.5 font-mono text-xs uppercase text-muted-foreground">
                      {c.kind}
                    </span>
                    <span className="font-mono text-sm">{c.name}</span>
                  </div>
                  <div className="flex items-center gap-4 text-xs text-muted-foreground">
                    <span>
                      {t("contributionsCommits", { count: c.commit_count })}
                    </span>
                    <span title={t("contributionsLastCommit")}>
                      {formatCommitDate(c.last_commit, locale)}
                    </span>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
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
