import { redirect } from "next/navigation";
import { signIn } from "@/auth";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

/**
 * /auth/signin — entry point that hands off to Keycloak.
 *
 * The page is a server component that exposes a single form-action button.
 * Clicking it triggers the OIDC code flow; the user lands back on
 * /auth/callback after authentication and is then redirected to
 * `redirect_to` if it was provided in the query string.
 */
export default async function SignInPage({
  searchParams,
}: {
  searchParams: Promise<{ redirect_to?: string }>;
}) {
  const params = await searchParams;
  const target = params.redirect_to ?? "/";

  return (
    <div className="container flex items-center justify-center py-16">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Sign in</CardTitle>
          <CardDescription>
            Use your Falabella identity to access authenticated portal features.
            Anonymous browsing of the catalog stays available without signing in.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            action={async () => {
              "use server";
              await signIn("keycloak", { redirectTo: target });
              // signIn redirects; this return is unreachable.
              redirect(target);
            }}
          >
            <Button type="submit" className="w-full">
              Continue with Keycloak
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
