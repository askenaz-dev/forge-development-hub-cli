"use client";

import * as React from "react";
import Link from "next/link";
import { ArrowLeft, ArrowRight, Check, SkipForward } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { CopyCommand } from "@/components/copy-command";

type DetectedOS = "darwin" | "linux" | "windows";

const STEPS: { id: string; title: string }[] = [
  { id: "detect-os", title: "Detect your OS" },
  { id: "download", title: "Download + checksum" },
  { id: "place-on-path", title: "Place on PATH" },
  { id: "configure-registry", title: "Configure registry" },
  { id: "doctor", title: "Run fdh doctor" },
  { id: "install-starter", title: "Install your first skill" },
  { id: "confirm-in-agent", title: "Confirm in your agent" },
];

/**
 * OnboardingWizard — single client component that handles all 7 steps.
 *
 * State strategy:
 *   - currentStep lives in component state + a cookie so reload resumes.
 *   - On every step completion (or skip) we POST to /api/v1/activation
 *     so admins can see the funnel.
 *   - The session id is stable across reloads (cookie); we don't try to
 *     correlate across browsers — that's an analytics concern, not wizard.
 */
export function OnboardingWizard({
  initialStep,
  sessionId,
  detectedOS,
}: {
  initialStep: number;
  sessionId: string;
  detectedOS: DetectedOS;
}) {
  const [step, setStep] = React.useState(initialStep);

  // Persist the session id + step in cookies (client-side, since we
  // already received the id from the server shell).
  React.useEffect(() => {
    document.cookie = `fdh-wizard-session=${sessionId}; path=/; max-age=2592000; samesite=lax`;
  }, [sessionId]);
  React.useEffect(() => {
    document.cookie = `fdh-wizard-step=${step}; path=/; max-age=2592000; samesite=lax`;
  }, [step]);

  // Fire activation event on step entry.
  React.useEffect(() => {
    void emitActivation({
      step: STEPS[step]?.id ?? "unknown",
      wizard_session_id: sessionId,
      os: detectedOS,
    });
  }, [step, sessionId, detectedOS]);

  const goNext = () => setStep((s) => Math.min(s + 1, STEPS.length - 1));
  const goBack = () => setStep((s) => Math.max(s - 1, 0));
  const skip = () => {
    void emitActivation({
      step: `skip-${STEPS[step]?.id ?? "unknown"}`,
      wizard_session_id: sessionId,
      os: detectedOS,
    });
    setStep(STEPS.length - 1);
  };

  return (
    <div className="mx-auto mt-8 max-w-3xl">
      {/* Progress bar */}
      <ol className="mb-8 flex flex-wrap items-center justify-between gap-y-2 text-xs">
        {STEPS.map((s, i) => (
          <li key={s.id} className="flex items-center gap-2">
            <span
              className={`inline-flex h-6 w-6 items-center justify-center rounded-full border ${
                i < step
                  ? "border-primary bg-primary text-primary-foreground"
                  : i === step
                  ? "border-primary text-primary"
                  : "border-border text-muted-foreground"
              }`}
            >
              {i < step ? <Check className="h-3 w-3" /> : i + 1}
            </span>
            <span
              className={
                i === step
                  ? "font-medium text-foreground"
                  : "text-muted-foreground"
              }
            >
              {s.title}
            </span>
          </li>
        ))}
      </ol>

      {/* Step body */}
      <div>{renderStep(step, detectedOS)}</div>

      {/* Footer nav */}
      <div className="mt-8 flex items-center justify-between">
        <Button variant="ghost" onClick={goBack} disabled={step === 0}>
          <ArrowLeft className="h-4 w-4" /> Back
        </Button>
        <div className="flex items-center gap-2">
          {step < STEPS.length - 1 && (
            <Button variant="ghost" onClick={skip}>
              <SkipForward className="h-4 w-4" /> Skip walkthrough
            </Button>
          )}
          {step < STEPS.length - 1 ? (
            <Button onClick={goNext}>
              Next <ArrowRight className="h-4 w-4" />
            </Button>
          ) : (
            <Button asChild>
              <Link href="/skills">Browse all skills</Link>
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}

function renderStep(step: number, os: DetectedOS) {
  const osLabel = os === "darwin" ? "macOS" : os === "linux" ? "Linux" : "Windows";

  switch (step) {
    case 0:
      return (
        <Card>
          <CardHeader>
            <CardTitle>1. Detect your OS</CardTitle>
            <CardDescription>
              We detected you're on <strong>{osLabel}</strong>. The Install page
              has the matching commands first.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <p className="text-sm">
              If you're installing on a different machine (a remote dev box, a
              CI runner), pick the matching tab on the install page.
            </p>
            <Button asChild variant="outline">
              <Link href="/install">Open the install page</Link>
            </Button>
          </CardContent>
        </Card>
      );

    case 1:
      return (
        <Card>
          <CardHeader>
            <CardTitle>2. Download the binary and verify its checksum</CardTitle>
            <CardDescription>
              Each release tar.gz ships with an adjacent <code>.sha256</code>{" "}
              file. Verify before extracting — never run an unsigned binary
              you didn't checksum.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <p className="mb-2 text-sm">
              Detailed per-platform commands are on the install page; this
              wizard step is a checkpoint.
            </p>
            <Button asChild variant="outline">
              <Link href="/install">Open install commands</Link>
            </Button>
          </CardContent>
        </Card>
      );

    case 2:
      return (
        <Card>
          <CardHeader>
            <CardTitle>3. Place fdh on PATH and confirm</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <p className="text-sm">
              After extracting, move <code>fdh</code> to a directory on{" "}
              <code>PATH</code>. Then verify:
            </p>
            <CopyCommand command="fdh --version" />
            <p className="text-xs text-muted-foreground">
              Expected output: <code>fdh version v0.1.0 (commit ..., built ...)</code>
            </p>
          </CardContent>
        </Card>
      );

    case 3:
      return (
        <Card>
          <CardHeader>
            <CardTitle>4. Point fdh at the registry</CardTitle>
            <CardDescription>
              Tell <code>fdh</code> where to read skills from. The public
              askenaz-dev hub registry is the default.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <CopyCommand command="fdh config set registry.url https://github.com/askenaz-dev/forge-development-hub.git" />
            <p className="text-xs text-muted-foreground">
              For local development against the bundled fixture registry, use{" "}
              <code>fdh config set registry.local_path /path/to/registry</code>{" "}
              instead.
            </p>
          </CardContent>
        </Card>
      );

    case 4:
      return (
        <Card>
          <CardHeader>
            <CardTitle>5. Run fdh doctor</CardTitle>
            <CardDescription>
              Doctor detects which AI agents are installed on your machine and
              confirms the registry is reachable.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <CopyCommand command="fdh doctor" />
            <p className="text-xs text-muted-foreground">
              You should see at least one of <code>claude-code</code>,{" "}
              <code>copilot</code>, <code>codex</code>, <code>opencode</code>{" "}
              listed as <strong>DETECTED</strong>, plus the registry marked as{" "}
              <code>reachable</code>.
            </p>
          </CardContent>
        </Card>
      );

    case 5:
      return (
        <Card>
          <CardHeader>
            <CardTitle>6. Install a starter skill</CardTitle>
            <CardDescription>
              <code>code-review/checklist</code> is a good first skill for any
              repository — it gives every agent a consistent review pass.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <CopyCommand command="fdh install code-review/checklist" />
            <p className="text-xs text-muted-foreground">
              The command writes the bundle to every detected agent's
              directory and drops a <code>.skill-meta.yaml</code> sidecar so{" "}
              <code>fdh list</code> can track what's installed.
            </p>
          </CardContent>
        </Card>
      );

    case 6:
      return (
        <Card>
          <CardHeader>
            <CardTitle>7. Confirm in your agent</CardTitle>
            <CardDescription>
              Open the agent you use most and verify the skill appears.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <ul className="space-y-2 text-sm">
              <li>
                <strong>Claude Code</strong>: type <code>/</code> in the prompt
                — <code>/checklist</code> should be in the list.
              </li>
              <li>
                <strong>GitHub Copilot</strong>: ask Copilot Chat to "review
                this PR using the checklist" — it should reference the skill.
              </li>
              <li>
                <strong>Codex CLI</strong>: <code>codex /skills</code> — the
                checklist appears under <code>code-review</code>.
              </li>
              <li>
                <strong>OpenCode</strong>: <code>skill()</code> picker — the
                skill is selectable.
              </li>
            </ul>

            <div className="rounded-md border bg-card p-4">
              <h3 className="text-sm font-semibold">What's next</h3>
              <ul className="mt-2 space-y-1 text-sm text-muted-foreground">
                <li>
                  ·{" "}
                  <Link href="/skills" className="underline">
                    Browse more skills
                  </Link>{" "}
                  by SDLC phase (security, testing, operations, …).
                </li>
                <li>
                  ·{" "}
                  <Link href="/auth/signin" className="underline">
                    Sign in
                  </Link>{" "}
                  to unlock favorites + admin views.
                </li>
                <li>
                  · Share this onboarding link with a teammate:{" "}
                  <code>/onboarding</code>.
                </li>
              </ul>
            </div>
          </CardContent>
        </Card>
      );

    default:
      return null;
  }
}

interface ActivationPayload {
  step: string;
  wizard_session_id: string;
  os?: string;
  locale?: string;
}

async function emitActivation(payload: ActivationPayload) {
  try {
    await fetch("/api/onboarding/activation", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
      // Best-effort — wizard works even if the activation endpoint is down.
      keepalive: true,
    });
  } catch {
    // Swallow: telemetry is optional, wizard UX is not.
  }
}
