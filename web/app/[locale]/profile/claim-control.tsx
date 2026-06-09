"use client";

import * as React from "react";
import { useFormState, useFormStatus } from "react-dom";
import { Button } from "@/components/ui/button";
import { claimInstallAction, type ClaimState } from "./claim-action";

/**
 * Client island for the voluntary install-claim control (capability
 * hub-usage-telemetry, design D5, task 8.2).
 *
 * It owns nothing privileged: the form's `action` is the `claimInstallAction`
 * Server Action, which mints the service token and POSTs the claim ENTIRELY on
 * the server, binding it to the SESSION user's email (never anything this
 * component sends). The service token / client secret never enter this component
 * or the browser bundle — only the serializable `ClaimState` crosses back.
 *
 * Labels are props because a Client Component cannot call async
 * `getTranslations()`; the parent (server) page resolves them.
 */
export interface ClaimLabels {
  inputLabel: string;
  placeholder: string;
  button: string;
  pending: string;
  success: string;
  storeUnavailable: string;
  failureTemplate: string; // uses {detail}
  hint: string;
}

const INITIAL: ClaimState = { status: "idle" };

function fill(template: string, vars: Record<string, string>): string {
  return template.replace(/\{(\w+)\}/g, (_, k) =>
    k in vars ? String(vars[k]) : `{${k}}`
  );
}

function SubmitButton({ labels }: { labels: ClaimLabels }) {
  const { pending } = useFormStatus();
  return (
    <Button type="submit" disabled={pending} aria-busy={pending}>
      {pending ? labels.pending : labels.button}
    </Button>
  );
}

export function ClaimControl({ labels }: { labels: ClaimLabels }) {
  const [state, formAction] = useFormState(claimInstallAction, INITIAL);

  return (
    <form action={formAction} className="space-y-3">
      <div className="space-y-1.5">
        <label
          htmlFor="install_id"
          className="block text-xs font-medium uppercase tracking-wide text-muted-foreground"
        >
          {labels.inputLabel}
        </label>
        <div className="flex flex-wrap items-center gap-2">
          <input
            id="install_id"
            name="install_id"
            type="text"
            required
            autoComplete="off"
            placeholder={labels.placeholder}
            className="min-w-0 flex-1 rounded-md border bg-background px-3 py-2 font-mono text-sm shadow-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
          <SubmitButton labels={labels} />
        </div>
        <p className="text-xs text-muted-foreground">{labels.hint}</p>
      </div>

      {state.status === "success" && (
        <p
          role="status"
          className="text-sm text-emerald-600 dark:text-emerald-500"
        >
          {labels.success}
        </p>
      )}
      {state.status === "store_unavailable" && (
        <p role="status" className="text-sm text-amber-700 dark:text-amber-400">
          {labels.storeUnavailable}
        </p>
      )}
      {state.status === "error" && (
        <p role="alert" className="text-sm text-destructive">
          {fill(labels.failureTemplate, { detail: state.detail ?? "unknown" })}
        </p>
      )}
    </form>
  );
}
