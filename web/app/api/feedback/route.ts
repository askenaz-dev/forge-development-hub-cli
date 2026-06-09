import { NextRequest, NextResponse } from "next/server";

/**
 * Anonymous feedback forwarding shim (capability hub-usage-telemetry, task 7.1,
 * design D2/D8).
 *
 * The browser POSTs to this same-origin URL; the Next.js server forwards it to
 * the Go portal's ANONYMOUS telemetry ingest as an `event=feedback` event. This
 * mirrors `app/api/onboarding/activation/route.ts` exactly: no auth, no service
 * token, no identity — ingest is anonymous by contract (a forwarded service
 * token would confer no privilege there anyway). It only keeps CORS off the
 * table for the browser and normalizes the payload into the telemetry schema.
 *
 * The telemetry event schema is a CLOSED allow-list, strict-decoded by the API
 * (unknown fields → 400). So we forward ONLY the fields the schema accepts:
 * `event` (forced to "feedback"), `rating`, `category`, `text`, and `locale`.
 * Anything else the client sends is dropped here rather than risking a 400.
 */
const API_BASE = process.env.FDH_API_BASE_URL ?? "http://localhost:8080";

interface FeedbackBody {
  rating?: number;
  category?: string;
  text?: string;
  locale?: string;
}

export async function POST(request: NextRequest) {
  try {
    const raw = (await request.json().catch(() => ({}))) as FeedbackBody;

    // Build a minimal, schema-conformant feedback event. `event` is forced;
    // unknown client fields are intentionally not forwarded (strict decode).
    const payload: Record<string, unknown> = { event: "feedback" };
    if (typeof raw.rating === "number") payload.rating = raw.rating;
    if (typeof raw.category === "string" && raw.category) {
      payload.category = raw.category;
    }
    if (typeof raw.text === "string" && raw.text) payload.text = raw.text;
    if (typeof raw.locale === "string" && raw.locale) {
      payload.locale = raw.locale;
    }

    const upstream = await fetch(`${API_BASE}/api/v1/telemetry`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    return NextResponse.json(await upstream.json().catch(() => ({})), {
      status: upstream.status,
    });
  } catch (err) {
    return NextResponse.json(
      { error: "forward_failed", message: String(err) },
      { status: 502 }
    );
  }
}
