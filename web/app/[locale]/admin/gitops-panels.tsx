"use client";

import * as React from "react";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

/**
 * Phase-3 GitOps write panels (capability portal-gitops-write).
 *
 * Three client islands — Import (author+), Harness editor (publisher+), Curate
 * (admin) — that POST to the same-origin BFF routes under `/api/gitops/*`. The
 * BFF re-checks the role server-side and forwards to the Go API with the service
 * credential; NOTHING privileged lives here. Every panel renders the same set of
 * outcome states from the JSON the route returns:
 *
 *   - 201 { pr_url }                  → "PR opened: <url>" (propose-only)
 *   - 200 { pr_url, already_open:true }→ "A change is already open: <url>"
 *   - 503 gitops_not_configured       → a CALM "GitHub App not configured yet"
 *                                       notice (the surface ships dark)
 *   - 403 forbidden                   → forbidden notice
 *   - 422 <code> + message            → the NAMED validation/lifecycle error
 *   - any other / network             → a terse failure
 *
 * Each panel is also gated by the VIEWER role passed from the server page
 * (`role`), so a publisher never sees the curate controls and an author never
 * sees the harness editor — matching what the BFF/API will enforce anyway.
 *
 * Every panel carries the load-bearing notice: it PROPOSES a PR; nothing is live
 * until a human reviews and merges it (the bot can open but never merge).
 *
 * All labels are passed in as props (a Client Component cannot call the async
 * `getTranslations()`); the server page resolves them.
 */

// --- Shared label bundles ----------------------------------------------------

export interface GitopsCommonLabels {
  proposeNotice: string;
  submit: string;
  pending: string;
  /** "{url}" placeholder — the opened PR url. */
  prOpenedTemplate: string;
  /** "{url}" placeholder — the pre-existing open PR url. */
  alreadyOpenTemplate: string;
  notConfigured: string;
  forbidden: string;
  /** "{detail}" placeholder. */
  failureTemplate: string;
  viewPr: string;
}

export interface ImportLabels {
  title: string;
  description: string;
  nameLabel: string;
  namePlaceholder: string;
  descLabel: string;
  descPlaceholder: string;
  ownerTeamLabel: string;
  ownerTeamPlaceholder: string;
  agentsLabel: string;
  agentsHint: string;
  zipLabel: string;
  zipHint: string;
  modeForm: string;
  modeZip: string;
  nameRequired: string;
  zipRequired: string;
}

export interface HarnessLabels {
  title: string;
  description: string;
  harnessLabel: string;
  harnessPlaceholder: string;
  ownerTeamLabel: string;
  descLabel: string;
  addHeading: string;
  removeHeading: string;
  emptyCatalog: string;
  noChanges: string;
  /** "{kind}" "{name}" placeholders — for the unknown-component client guard. */
  unknownComponentTemplate: string;
}

export interface CurateLabels {
  title: string;
  description: string;
  componentLabel: string;
  componentPlaceholder: string;
  actionLabel: string;
  actionSetDefaultTrue: string;
  actionSetDefaultFalse: string;
  actionDeprecate: string;
  actionYank: string;
  versionLabel: string;
  versionPlaceholder: string;
  versionRequired: string;
  noUnyankNote: string;
  componentRequired: string;
}

const KIND_LABELS: Record<string, string> = {
  skill: "skills",
  rule: "rules",
  agent: "agents",
  hook: "hooks",
};

/** A catalog component reference the harness/curate panels operate over. */
export interface CatalogRef {
  kind: "skill" | "rule" | "agent" | "hook";
  name: string;
}

// --- Outcome state -----------------------------------------------------------

type Outcome =
  | { status: "idle" }
  | { status: "pending" }
  | { status: "opened"; url: string }
  | { status: "alreadyOpen"; url: string }
  | { status: "notConfigured" }
  | { status: "forbidden" }
  | { status: "validation"; message: string }
  | { status: "error"; detail: string };

function fill(template: string, vars: Record<string, string>): string {
  return template.replace(/\{(\w+)\}/g, (_, k) =>
    k in vars ? String(vars[k]) : `{${k}}`
  );
}

/**
 * Translate a BFF route Response into an `Outcome`. The route mirrors the Go API
 * status codes, so the same mapping serves every panel.
 */
async function outcomeFrom(res: Response): Promise<Outcome> {
  if (res.status === 503) {
    return { status: "notConfigured" };
  }
  if (res.status === 403) {
    return { status: "forbidden" };
  }
  if (res.status === 422) {
    const body = await res.json().catch(() => ({}) as { message?: string });
    return { status: "validation", message: body.message ?? "validation failed" };
  }
  if (res.ok) {
    const body = (await res.json()) as { pr_url: string; already_open: boolean };
    return body.already_open
      ? { status: "alreadyOpen", url: body.pr_url }
      : { status: "opened", url: body.pr_url };
  }
  const body = await res.json().catch(() => ({}) as { message?: string });
  return { status: "error", detail: body.message ?? `HTTP ${res.status}` };
}

/** Shared result line under every panel form. */
function OutcomeNotice({
  outcome,
  labels,
}: {
  outcome: Outcome;
  labels: GitopsCommonLabels;
}) {
  if (outcome.status === "idle" || outcome.status === "pending") return null;

  if (outcome.status === "opened" || outcome.status === "alreadyOpen") {
    const template =
      outcome.status === "opened"
        ? labels.prOpenedTemplate
        : labels.alreadyOpenTemplate;
    const [before, after] = splitOnPlaceholder(template);
    return (
      <p
        role="status"
        className="text-sm text-emerald-600 dark:text-emerald-500"
      >
        {before}
        <a
          href={outcome.url}
          target="_blank"
          rel="noopener noreferrer"
          className="font-medium underline underline-offset-2"
        >
          {labels.viewPr}
        </a>
        {after}
      </p>
    );
  }

  if (outcome.status === "notConfigured") {
    return (
      <p role="status" className="text-sm text-amber-700 dark:text-amber-400">
        {labels.notConfigured}
      </p>
    );
  }
  if (outcome.status === "forbidden") {
    return (
      <p role="alert" className="text-sm text-destructive">
        {labels.forbidden}
      </p>
    );
  }
  if (outcome.status === "validation") {
    return (
      <p role="alert" className="text-sm text-destructive">
        {outcome.message}
      </p>
    );
  }
  // error
  return (
    <p role="alert" className="text-sm text-destructive">
      {fill(labels.failureTemplate, { detail: outcome.detail })}
    </p>
  );
}

/** Split a "{url}" template into the text before/after the placeholder. */
function splitOnPlaceholder(template: string): [string, string] {
  const idx = template.indexOf("{url}");
  if (idx < 0) return [template, ""];
  return [template.slice(0, idx), template.slice(idx + "{url}".length)];
}

/** The always-visible "Proposes a PR — not live until merged" banner. */
function ProposeBanner({ text }: { text: string }) {
  return (
    <div
      role="note"
      className="rounded-md border border-sky-500/40 bg-sky-500/10 px-3 py-2 text-xs font-medium text-sky-700 dark:text-sky-400"
    >
      {text}
    </div>
  );
}

function FieldLabel({
  htmlFor,
  children,
}: {
  htmlFor?: string;
  children: React.ReactNode;
}) {
  return (
    <label
      htmlFor={htmlFor}
      className="block text-xs font-medium uppercase tracking-wide text-muted-foreground"
    >
      {children}
    </label>
  );
}

// --- Import panel ------------------------------------------------------------

const AGENT_IDS = ["claude-code", "codex", "copilot", "opencode"];

export function ImportPanel({
  labels,
  common,
}: {
  labels: ImportLabels;
  common: GitopsCommonLabels;
}) {
  const [mode, setMode] = React.useState<"form" | "zip">("form");
  const [name, setName] = React.useState("");
  const [description, setDescription] = React.useState("");
  const [ownerTeam, setOwnerTeam] = React.useState("");
  const [agents, setAgents] = React.useState<string[]>([]);
  const [zip, setZip] = React.useState<File | null>(null);
  const [outcome, setOutcome] = React.useState<Outcome>({ status: "idle" });

  const toggleAgent = (id: string) =>
    setAgents((prev) =>
      prev.includes(id) ? prev.filter((a) => a !== id) : [...prev, id]
    );

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!name.trim()) {
      setOutcome({ status: "validation", message: labels.nameRequired });
      return;
    }
    setOutcome({ status: "pending" });
    try {
      let res: Response;
      if (mode === "zip") {
        if (!zip) {
          setOutcome({ status: "validation", message: labels.zipRequired });
          return;
        }
        const fd = new FormData();
        fd.set("kind", "skill");
        fd.set("name", name.trim());
        if (ownerTeam.trim()) fd.set("owner_team", ownerTeam.trim());
        if (agents.length) fd.set("agents", agents.join(","));
        fd.set("bundle", zip, zip.name);
        res = await fetch("/api/gitops/import", { method: "POST", body: fd });
      } else {
        res = await fetch("/api/gitops/import", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            kind: "skill",
            name: name.trim(),
            description: description.trim() || undefined,
            owner_team: ownerTeam.trim() || undefined,
            agents: agents.length ? agents : undefined,
          }),
        });
      }
      setOutcome(await outcomeFrom(res));
    } catch (err) {
      setOutcome({
        status: "error",
        detail: err instanceof Error ? err.message : "network error",
      });
    }
  }

  const pending = outcome.status === "pending";

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{labels.title}</CardTitle>
        <CardDescription>{labels.description}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <ProposeBanner text={common.proposeNotice} />

        <div className="inline-flex rounded-md border p-0.5 text-sm">
          <button
            type="button"
            onClick={() => setMode("form")}
            className={`rounded px-3 py-1 ${mode === "form" ? "bg-primary text-primary-foreground" : "text-muted-foreground"}`}
          >
            {labels.modeForm}
          </button>
          <button
            type="button"
            onClick={() => setMode("zip")}
            className={`rounded px-3 py-1 ${mode === "zip" ? "bg-primary text-primary-foreground" : "text-muted-foreground"}`}
          >
            {labels.modeZip}
          </button>
        </div>

        <form onSubmit={onSubmit} className="space-y-4">
          <div className="space-y-1.5">
            <FieldLabel htmlFor="import-name">{labels.nameLabel}</FieldLabel>
            <Input
              id="import-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={labels.namePlaceholder}
              autoComplete="off"
              className="font-mono"
            />
          </div>

          {mode === "form" ? (
            <div className="space-y-1.5">
              <FieldLabel htmlFor="import-desc">{labels.descLabel}</FieldLabel>
              <textarea
                id="import-desc"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder={labels.descPlaceholder}
                rows={3}
                className="flex w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              />
            </div>
          ) : (
            <div className="space-y-1.5">
              <FieldLabel htmlFor="import-zip">{labels.zipLabel}</FieldLabel>
              <input
                id="import-zip"
                type="file"
                accept=".zip,application/zip"
                onChange={(e) => setZip(e.target.files?.[0] ?? null)}
                className="block w-full text-sm text-muted-foreground file:mr-3 file:rounded-md file:border file:border-input file:bg-background file:px-3 file:py-1.5 file:text-sm file:font-medium"
              />
              <p className="text-xs text-muted-foreground">{labels.zipHint}</p>
            </div>
          )}

          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-1.5">
              <FieldLabel htmlFor="import-owner">
                {labels.ownerTeamLabel}
              </FieldLabel>
              <Input
                id="import-owner"
                value={ownerTeam}
                onChange={(e) => setOwnerTeam(e.target.value)}
                placeholder={labels.ownerTeamPlaceholder}
                autoComplete="off"
              />
            </div>
            <div className="space-y-1.5">
              <FieldLabel>{labels.agentsLabel}</FieldLabel>
              <div className="flex flex-wrap gap-2">
                {AGENT_IDS.map((id) => (
                  <label
                    key={id}
                    className="inline-flex cursor-pointer items-center gap-1.5 rounded-md border px-2 py-1 text-xs font-mono"
                  >
                    <input
                      type="checkbox"
                      checked={agents.includes(id)}
                      onChange={() => toggleAgent(id)}
                    />
                    {id}
                  </label>
                ))}
              </div>
              <p className="text-xs text-muted-foreground">{labels.agentsHint}</p>
            </div>
          </div>

          <Button type="submit" disabled={pending} aria-busy={pending}>
            {pending ? common.pending : common.submit}
          </Button>
        </form>

        <OutcomeNotice outcome={outcome} labels={common} />
      </CardContent>
    </Card>
  );
}

// --- Harness editor ----------------------------------------------------------

const HARNESS_KINDS: CatalogRef["kind"][] = ["skill", "rule", "agent", "hook"];

export function HarnessPanel({
  labels,
  common,
  catalog,
}: {
  labels: HarnessLabels;
  common: GitopsCommonLabels;
  catalog: CatalogRef[];
}) {
  const [harness, setHarness] = React.useState("");
  const [ownerTeam, setOwnerTeam] = React.useState("");
  const [description, setDescription] = React.useState("");
  // Per-kind add / remove selections, keyed by "kind:name".
  const [adds, setAdds] = React.useState<Set<string>>(new Set());
  const [removes, setRemoves] = React.useState<Set<string>>(new Set());
  const [outcome, setOutcome] = React.useState<Outcome>({ status: "idle" });

  const byKind = React.useMemo(() => {
    const m: Record<string, string[]> = { skill: [], rule: [], agent: [], hook: [] };
    for (const c of catalog) m[c.kind]?.push(c.name);
    for (const k of Object.keys(m)) m[k]?.sort();
    return m;
  }, [catalog]);

  const catalogKeys = React.useMemo(
    () => new Set(catalog.map((c) => `${c.kind}:${c.name}`)),
    [catalog]
  );

  const toggle = (
    set: Set<string>,
    setter: React.Dispatch<React.SetStateAction<Set<string>>>,
    key: string,
    other: Set<string>,
    otherSetter: React.Dispatch<React.SetStateAction<Set<string>>>
  ) => {
    const next = new Set(set);
    if (next.has(key)) next.delete(key);
    else {
      next.add(key);
      // Adding to one side clears the same key on the other (can't add+remove).
      if (other.has(key)) {
        const o = new Set(other);
        o.delete(key);
        otherSetter(o);
      }
    }
    setter(next);
  };

  const collect = (set: Set<string>, kind: string): string[] =>
    [...set]
      .filter((k) => k.startsWith(`${kind}:`))
      .map((k) => k.slice(kind.length + 1));

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!harness.trim()) {
      setOutcome({ status: "validation", message: labels.harnessPlaceholder });
      return;
    }
    // Client-side guard: reject any ADDED reference not in the catalog (the API
    // re-checks, but we fail fast and name it — spec scenario).
    for (const key of adds) {
      if (!catalogKeys.has(key)) {
        const [kind = "", name = ""] = key.split(":");
        setOutcome({
          status: "validation",
          message: fill(labels.unknownComponentTemplate, { kind, name }),
        });
        return;
      }
    }
    const payload: Record<string, unknown> = { harness: harness.trim() };
    if (ownerTeam.trim()) payload.owner_team = ownerTeam.trim();
    if (description.trim()) payload.description = description.trim();
    for (const kind of HARNESS_KINDS) {
      const addList = collect(adds, kind);
      const rmList = collect(removes, kind);
      const plural = kind === "skill" ? "skills" : kind === "rule" ? "rules" : kind === "agent" ? "agents" : "hooks";
      if (addList.length) payload[`add_${plural}`] = addList;
      if (rmList.length) payload[`remove_${plural}`] = rmList;
    }
    const hasChange =
      "owner_team" in payload ||
      "description" in payload ||
      HARNESS_KINDS.some((k) => {
        const plural = k === "skill" ? "skills" : k === "rule" ? "rules" : k === "agent" ? "agents" : "hooks";
        return `add_${plural}` in payload || `remove_${plural}` in payload;
      });
    if (!hasChange) {
      setOutcome({ status: "validation", message: labels.noChanges });
      return;
    }

    setOutcome({ status: "pending" });
    try {
      const res = await fetch("/api/gitops/harness", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      setOutcome(await outcomeFrom(res));
    } catch (err) {
      setOutcome({
        status: "error",
        detail: err instanceof Error ? err.message : "network error",
      });
    }
  }

  const pending = outcome.status === "pending";

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{labels.title}</CardTitle>
        <CardDescription>{labels.description}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <ProposeBanner text={common.proposeNotice} />

        <form onSubmit={onSubmit} className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-1.5">
              <FieldLabel htmlFor="harness-name">{labels.harnessLabel}</FieldLabel>
              <Input
                id="harness-name"
                value={harness}
                onChange={(e) => setHarness(e.target.value)}
                placeholder={labels.harnessPlaceholder}
                autoComplete="off"
                className="font-mono"
              />
            </div>
            <div className="space-y-1.5">
              <FieldLabel htmlFor="harness-owner">
                {labels.ownerTeamLabel}
              </FieldLabel>
              <Input
                id="harness-owner"
                value={ownerTeam}
                onChange={(e) => setOwnerTeam(e.target.value)}
                autoComplete="off"
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <FieldLabel htmlFor="harness-desc">{labels.descLabel}</FieldLabel>
            <Input
              id="harness-desc"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              autoComplete="off"
            />
          </div>

          {catalog.length === 0 ? (
            <p className="text-sm text-muted-foreground">{labels.emptyCatalog}</p>
          ) : (
            <div className="space-y-3">
              {HARNESS_KINDS.map((kind) => {
                const names = byKind[kind];
                if (!names || names.length === 0) return null;
                return (
                  <div key={kind} className="rounded-md border p-3">
                    <h4 className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                      {KIND_LABELS[kind]}
                    </h4>
                    <div className="flex flex-wrap gap-2">
                      {names.map((n) => {
                        const key = `${kind}:${n}`;
                        const added = adds.has(key);
                        const removed = removes.has(key);
                        return (
                          <span
                            key={key}
                            className={`inline-flex items-center gap-1 rounded-md border px-2 py-1 font-mono text-xs ${
                              added
                                ? "border-emerald-500/50 bg-emerald-500/10"
                                : removed
                                  ? "border-destructive/50 bg-destructive/10"
                                  : ""
                            }`}
                          >
                            {n}
                            <button
                              type="button"
                              title={labels.addHeading}
                              onClick={() =>
                                toggle(adds, setAdds, key, removes, setRemoves)
                              }
                              className={`px-1 ${added ? "text-emerald-600" : "text-muted-foreground"}`}
                            >
                              +
                            </button>
                            <button
                              type="button"
                              title={labels.removeHeading}
                              onClick={() =>
                                toggle(removes, setRemoves, key, adds, setAdds)
                              }
                              className={`px-1 ${removed ? "text-destructive" : "text-muted-foreground"}`}
                            >
                              −
                            </button>
                          </span>
                        );
                      })}
                    </div>
                  </div>
                );
              })}
            </div>
          )}

          <Button type="submit" disabled={pending} aria-busy={pending}>
            {pending ? common.pending : common.submit}
          </Button>
        </form>

        <OutcomeNotice outcome={outcome} labels={common} />
      </CardContent>
    </Card>
  );
}

// --- Curate panel ------------------------------------------------------------

type CurateActionKey =
  | "default_true"
  | "default_false"
  | "deprecate"
  | "yank";

export function CuratePanel({
  labels,
  common,
  catalog,
}: {
  labels: CurateLabels;
  common: GitopsCommonLabels;
  catalog: CatalogRef[];
}) {
  const [selected, setSelected] = React.useState("");
  const [action, setAction] = React.useState<CurateActionKey>("default_true");
  const [version, setVersion] = React.useState("");
  const [outcome, setOutcome] = React.useState<Outcome>({ status: "idle" });

  const options = React.useMemo(
    () =>
      [...catalog]
        .map((c) => `${c.kind}:${c.name}`)
        .sort((a, b) => a.localeCompare(b)),
    [catalog]
  );

  const needsVersion = action === "deprecate" || action === "yank";

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!selected) {
      setOutcome({ status: "validation", message: labels.componentRequired });
      return;
    }
    if (needsVersion && !version.trim()) {
      setOutcome({ status: "validation", message: labels.versionRequired });
      return;
    }
    const [kind = "", name = ""] = selected.split(":");
    const payload: Record<string, unknown> = { kind, name };
    if (action === "default_true") payload.set_default = true;
    else if (action === "default_false") payload.set_default = false;
    else if (action === "deprecate") {
      payload.lifecycle = "deprecate";
      payload.version = version.trim();
    } else if (action === "yank") {
      payload.lifecycle = "yank";
      payload.version = version.trim();
    }

    setOutcome({ status: "pending" });
    try {
      const res = await fetch("/api/gitops/curate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      setOutcome(await outcomeFrom(res));
    } catch (err) {
      setOutcome({
        status: "error",
        detail: err instanceof Error ? err.message : "network error",
      });
    }
  }

  const pending = outcome.status === "pending";

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{labels.title}</CardTitle>
        <CardDescription>{labels.description}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <ProposeBanner text={common.proposeNotice} />

        <form onSubmit={onSubmit} className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-1.5">
              <FieldLabel htmlFor="curate-component">
                {labels.componentLabel}
              </FieldLabel>
              <select
                id="curate-component"
                value={selected}
                onChange={(e) => setSelected(e.target.value)}
                className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <option value="">{labels.componentPlaceholder}</option>
                {options.map((o) => (
                  <option key={o} value={o}>
                    {o}
                  </option>
                ))}
              </select>
            </div>
            <div className="space-y-1.5">
              <FieldLabel htmlFor="curate-action">{labels.actionLabel}</FieldLabel>
              <select
                id="curate-action"
                value={action}
                onChange={(e) => setAction(e.target.value as CurateActionKey)}
                className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <option value="default_true">{labels.actionSetDefaultTrue}</option>
                <option value="default_false">
                  {labels.actionSetDefaultFalse}
                </option>
                <option value="deprecate">{labels.actionDeprecate}</option>
                <option value="yank">{labels.actionYank}</option>
              </select>
            </div>
          </div>

          {needsVersion && (
            <div className="space-y-1.5">
              <FieldLabel htmlFor="curate-version">{labels.versionLabel}</FieldLabel>
              <Input
                id="curate-version"
                value={version}
                onChange={(e) => setVersion(e.target.value)}
                placeholder={labels.versionPlaceholder}
                autoComplete="off"
                className="font-mono"
              />
            </div>
          )}

          <p className="text-xs text-muted-foreground">{labels.noUnyankNote}</p>

          <Button type="submit" disabled={pending} aria-busy={pending}>
            {pending ? common.pending : common.submit}
          </Button>
        </form>

        <OutcomeNotice outcome={outcome} labels={common} />
      </CardContent>
    </Card>
  );
}
