import { redirect } from "next/navigation";
import { auth } from "@/auth";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { getInsights, type InsightsSummary, type KV } from "@/lib/api";

/**
 * /admin — auth-gated and role-gated to `admin`.
 *
 * The admin shell hosts admin-only views: registry refresh and the usage
 * insights summary (downloads, demand gaps, churn, install failures,
 * feedback). Insights surface here in the existing portal admin UI; there is
 * no separate analytics frontend.
 */
export default async function AdminPage() {
  const session = await auth();
  if (!session) redirect("/auth/signin?redirect_to=/admin");

  const groups: string[] = session.user?.groups ?? [];
  // Coarse role check at the page level until the role-map is exposed
  // via /api/v1/auth/me; the Go API still enforces the role on every call.
  const isAdmin = groups.includes("fdh-admins");
  if (!isAdmin) {
    return (
      <div className="container py-12">
        <h1 className="text-3xl font-bold tracking-tight">Admin</h1>
        <p className="mt-2 text-muted-foreground">
          Your account does not have the <code>fdh-admins</code> group.
        </p>
      </div>
    );
  }

  let insights: InsightsSummary | null = null;
  let insightsError: string | null = null;
  try {
    insights = await getInsights({ token: session.accessToken, revalidate: 0 });
  } catch (err) {
    insightsError = err instanceof Error ? err.message : String(err);
  }

  return (
    <div className="container py-12">
      <h1 className="text-3xl font-bold tracking-tight">Admin</h1>
      <p className="mt-2 text-muted-foreground">
        Usage insights and operational controls for the portal.
      </p>

      <h2 className="mt-10 text-xl font-semibold tracking-tight">Usage insights</h2>
      {insightsError && (
        <p className="mt-2 text-sm text-muted-foreground">
          Insights unavailable: <code>{insightsError}</code>
        </p>
      )}
      {insights && (
        <>
          <p className="mt-1 text-xs text-muted-foreground">
            {insights.total} events
            {insights.window_start && insights.window_end
              ? ` · ${new Date(insights.window_start).toLocaleString()} – ${new Date(
                  insights.window_end
                ).toLocaleString()}`
              : ""}
          </p>
          <div className="mt-4 grid gap-4 md:grid-cols-2 lg:grid-cols-3">
            <RankCard title="Top downloads" rows={insights.top_downloads} empty="No downloads yet." />
            <RankCard
              title="Demand gaps (zero-result searches)"
              rows={insights.demand_gaps}
              empty="No empty searches recorded."
            />
            <RankCard title="Top installs" rows={insights.top_installs} empty="No installs recorded." />
            <RankCard title="Uninstalls (churn)" rows={insights.top_uninstalls} empty="No uninstalls." />
            <RankCard
              title="Broken references (404s)"
              rows={insights.top_not_found}
              empty="No missing-component requests."
            />
            <MapCard
              title="Install failures by class"
              data={insights.install_failures_by_class}
              empty="No install failures."
            />
            <MapCard title="Feedback" data={insights.feedback} empty="No feedback yet." />
          </div>
        </>
      )}

      <h2 className="mt-12 text-xl font-semibold tracking-tight">Controls</h2>
      <div className="mt-4 grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Registry</CardTitle>
            <CardDescription>
              Force an immediate refresh against the Git registry.
            </CardDescription>
          </CardHeader>
          <CardContent className="text-sm text-muted-foreground">
            POST to <code>/api/v1/refresh</code> with a publisher+ token.
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function RankCard({ title, rows, empty }: { title: string; rows: KV[]; empty: string }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{title}</CardTitle>
      </CardHeader>
      <CardContent className="text-sm">
        {rows && rows.length > 0 ? (
          <ul className="space-y-1">
            {rows.map((r) => (
              <li key={r.key} className="flex justify-between gap-3">
                <span className="truncate font-mono text-xs">{r.key}</span>
                <span className="tabular-nums text-muted-foreground">{r.count}</span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-muted-foreground">{empty}</p>
        )}
      </CardContent>
    </Card>
  );
}

function MapCard({
  title,
  data,
  empty,
}: {
  title: string;
  data: Record<string, number>;
  empty: string;
}) {
  const entries = Object.entries(data ?? {}).sort((a, b) => b[1] - a[1]);
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{title}</CardTitle>
      </CardHeader>
      <CardContent className="text-sm">
        {entries.length > 0 ? (
          <ul className="space-y-1">
            {entries.map(([k, v]) => (
              <li key={k} className="flex justify-between gap-3">
                <span className="truncate font-mono text-xs">{k}</span>
                <span className="tabular-nums text-muted-foreground">{v}</span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-muted-foreground">{empty}</p>
        )}
      </CardContent>
    </Card>
  );
}
