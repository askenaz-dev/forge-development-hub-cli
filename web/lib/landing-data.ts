import { listComponents, type ComponentSummary, type Kind } from "@/lib/api";

/**
 * Landing data loader (portal-web "narrative landing with live catalog data").
 *
 * One server-side call to the catalog API, cached via ISR (`revalidate`). The
 * landing renders REAL component samples and REAL counts. If the API is
 * unavailable at render time we return a safe, fully-static fallback so the
 * landing never shows an error or an empty hero — it simply omits live numbers
 * and shows no catalog sample (the rest of the narrative still renders).
 */

export interface LandingData {
  /** true when the numbers/sample below came from the live API. */
  live: boolean;
  total: number;
  byKind: Record<Kind, number>;
  /** a small sample for the "live catalog" section. */
  sample: ComponentSummary[];
}

const KINDS: Kind[] = ["skill", "rule", "agent", "hook"];
const REVALIDATE_SECONDS = 300;
const SAMPLE_SIZE = 6;

const EMPTY_BY_KIND: Record<Kind, number> = { skill: 0, rule: 0, agent: 0, hook: 0 };

export async function getLandingData(): Promise<LandingData> {
  try {
    // Pull a generous page so counts are accurate for the current catalog
    // size (9 today); the API caps/paginates if it ever grows large.
    const page = await listComponents({ limit: 100 }, { revalidate: REVALIDATE_SECONDS });
    const items = page.items ?? [];
    const byKind = { ...EMPTY_BY_KIND };
    for (const c of items) {
      if (c.kind in byKind) byKind[c.kind] += 1;
    }
    // Prefer a diverse sample (one of each kind first, then fill).
    const sample = pickDiverseSample(items, SAMPLE_SIZE);
    return { live: true, total: items.length, byKind, sample };
  } catch {
    // Graceful fallback — never throw on the landing.
    return { live: false, total: 0, byKind: { ...EMPTY_BY_KIND }, sample: [] };
  }
}

function pickDiverseSample(items: ComponentSummary[], n: number): ComponentSummary[] {
  const out: ComponentSummary[] = [];
  const seen = new Set<string>();
  const key = (c: ComponentSummary) => `${c.kind}/${c.namespace}/${c.name}`;
  // First pass: one per kind for variety.
  for (const kind of KINDS) {
    const first = items.find((c) => c.kind === kind && !seen.has(key(c)));
    if (first) {
      out.push(first);
      seen.add(key(first));
    }
    if (out.length >= n) return out.slice(0, n);
  }
  // Second pass: fill the rest in catalog order.
  for (const c of items) {
    if (out.length >= n) break;
    if (!seen.has(key(c))) {
      out.push(c);
      seen.add(key(c));
    }
  }
  return out.slice(0, n);
}

export const AGENTS = ["Claude Code", "GitHub Copilot", "OpenAI Codex", "OpenCode"] as const;
export const SUPPORTED_AGENT_COUNT = AGENTS.length;
