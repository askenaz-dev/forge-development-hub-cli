import { cookies, headers } from "next/headers";
import { OnboardingWizard } from "./wizard";

/**
 * /onboarding — guided "first install" walkthrough.
 *
 * The page is a thin server shell that:
 *   1. Reads the User-Agent to suggest the right OS on step 1.
 *   2. Reads/creates a wizard-session-id cookie so progress resumes.
 *   3. Reads the saved step index from a cookie, so a returning user
 *      lands where they left off.
 *
 * The actual interactive flow is the OnboardingWizard client component.
 */
function detectOs(userAgent: string | undefined): "darwin" | "linux" | "windows" {
  const ua = (userAgent ?? "").toLowerCase();
  if (ua.includes("win")) return "windows";
  if (ua.includes("mac")) return "darwin";
  return "linux";
}

export default async function OnboardingPage() {
  const h = await headers();
  const ua = h.get("user-agent") ?? "";
  const cookieJar = await cookies();

  let sessionId = cookieJar.get("fdh-wizard-session")?.value;
  if (!sessionId) {
    // Generate a random id; the cookie set happens client-side on first
    // step interaction so server components stay pure.
    sessionId = `wsid_${Math.random().toString(36).slice(2)}${Date.now().toString(36)}`;
  }

  const savedStep = Number.parseInt(
    cookieJar.get("fdh-wizard-step")?.value ?? "0",
    10
  );
  const initialStep = Number.isFinite(savedStep) ? Math.max(0, Math.min(6, savedStep)) : 0;

  return (
    <div className="container py-10">
      <header className="mx-auto max-w-3xl text-center">
        <h1 className="text-3xl font-bold tracking-tight">Get started with FDH</h1>
        <p className="mt-2 text-muted-foreground">
          From zero to your first skill installed in your AI agent, in seven steps.
        </p>
      </header>
      <OnboardingWizard
        initialStep={initialStep}
        sessionId={sessionId}
        detectedOS={detectOs(ua)}
      />
    </div>
  );
}
