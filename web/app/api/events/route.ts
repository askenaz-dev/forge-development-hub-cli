import { NextRequest, NextResponse } from "next/server";

/**
 * Same-origin forwarding shim for product telemetry events (BFF rule).
 *
 * The browser POSTs to `/api/events`; the Next.js server forwards to the Go
 * portal's single ingestion endpoint `/api/v1/events`. The frontend never
 * addresses the portal (or any analytics backend) directly — it always talks
 * to a known origin. This also keeps CORS off the table.
 *
 * Forwarding is best-effort: a failure here must never break the page that
 * produced the event, so we always answer 200/202 to the browser and swallow
 * upstream errors (telemetry is not load-bearing).
 */
const API_BASE = process.env.FDH_API_BASE_URL ?? "http://localhost:8080";

export async function POST(request: NextRequest) {
  try {
    const body = await request.json();
    const upstream = await fetch(`${API_BASE}/api/v1/events`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    return NextResponse.json(await upstream.json().catch(() => ({})), {
      status: upstream.status,
    });
  } catch {
    // Never surface telemetry failures to the page.
    return NextResponse.json({ recorded: false }, { status: 202 });
  }
}
