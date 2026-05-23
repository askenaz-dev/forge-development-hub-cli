import { redirect } from "next/navigation";
import { auth } from "@/auth";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

/**
 * /profile — auth-gated. Displays the signed-in user's identity and
 * resolved portal role. Unauthenticated visitors are bounced to sign-in.
 *
 * In M6 the page only reads the session. The favorites + preferences
 * affordances are wired in later changes (`installer-write-flows` adds
 * favorites to the backend).
 */
export default async function ProfilePage() {
  const session = await auth();
  if (!session) {
    redirect("/auth/signin?redirect_to=/profile");
  }

  const groups: string[] = session.user?.groups ?? [];
  const preferredUsername: string | undefined = session.user?.preferredUsername;

  return (
    <div className="container py-12">
      <h1 className="text-3xl font-bold tracking-tight">Profile</h1>

      <div className="mt-8 grid gap-4 md:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Identity</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <Row label="Name" value={session.user?.name ?? "—"} />
            <Row label="Email" value={session.user?.email ?? "—"} />
            <Row label="Username" value={preferredUsername ?? "—"} />
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">Groups</CardTitle>
            <CardDescription>
              From your Keycloak token. The portal maps these to roles via the
              role-map config.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {groups.length === 0 ? (
              <p className="text-sm text-muted-foreground">No groups recorded.</p>
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
