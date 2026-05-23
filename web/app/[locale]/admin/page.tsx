import { redirect } from "next/navigation";
import { auth } from "@/auth";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

/**
 * /admin — auth-gated and role-gated to `admin`.
 *
 * The admin shell hosts admin-only views: registry refresh, activation
 * log, role overview. The MVP shows placeholders; later changes flesh
 * out the actual controls.
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

  return (
    <div className="container py-12">
      <h1 className="text-3xl font-bold tracking-tight">Admin</h1>
      <p className="mt-2 text-muted-foreground">
        Operational controls for the portal. Future changes (installer-write-flows,
        governance-full, scan-gate-ui) add the actual surface area here.
      </p>

      <div className="mt-8 grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Registry</CardTitle>
            <CardDescription>
              Force an immediate refresh against the Git registry.
            </CardDescription>
          </CardHeader>
          <CardContent className="text-sm text-muted-foreground">
            POST to <code>/api/v1/refresh</code> with a publisher+ token.
            (Inline UI lands in installer-write-flows.)
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">Activation log</CardTitle>
            <CardDescription>Recent onboarding wizard events.</CardDescription>
          </CardHeader>
          <CardContent className="text-sm text-muted-foreground">
            See <code>/api/v1/admin/activation</code>.
            (UI lands when analytics ingestion is decided in design.md Q5.)
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
