"use client";

import * as React from "react";
import { useFormState, useFormStatus } from "react-dom";
import { Button } from "@/components/ui/button";
import { refreshRegistryAction, type RefreshState } from "./refresh-action";

/**
 * Client island for the registry-refresh control.
 *
 * It owns nothing privileged: the form's `action` is the
 * `refreshRegistryAction` Server Action, which mints the Keycloak
 * client-credentials token and calls `POST /api/v1/refresh` ENTIRELY on the
 * server. The service token / client secret never enter this component or the
 * browser bundle — only the serializable `RefreshState` result does.
 *
 * Labels are passed in as props because a Client Component cannot call the
 * async `getTranslations()`; the parent (server) page resolves them.
 */

export interface RefreshLabels {
  button: string;
  pending: string;
  /** ICU-free templates; {placeholders} are substituted here. */
  successTemplate: string; // uses {at} {components} {skills}
  failureTemplate: string; // uses {detail}
}

const INITIAL: RefreshState = { status: "idle" };

function fill(template: string, vars: Record<string, string | number>): string {
  return template.replace(/\{(\w+)\}/g, (_, k) =>
    k in vars ? String(vars[k]) : `{${k}}`
  );
}

function SubmitButton({ labels }: { labels: RefreshLabels }) {
  const { pending } = useFormStatus();
  return (
    <Button type="submit" disabled={pending} aria-busy={pending}>
      {pending ? labels.pending : labels.button}
    </Button>
  );
}

export function RefreshControl({ labels }: { labels: RefreshLabels }) {
  const [state, formAction] = useFormState(refreshRegistryAction, INITIAL);

  return (
    <form action={formAction} className="space-y-3">
      <SubmitButton labels={labels} />

      {state.status === "success" && (
        <p
          role="status"
          className="text-sm text-emerald-600 dark:text-emerald-500"
        >
          {fill(labels.successTemplate, {
            at: state.refreshedAt ?? "—",
            components: state.componentCount ?? 0,
            skills: state.skillCount ?? 0,
          })}
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
