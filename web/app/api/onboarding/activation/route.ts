import { NextRequest, NextResponse } from "next/server";

/**
 * Forwarding shim that lets the wizard POST to a same-origin URL.
 *
 * The browser fetches `/api/onboarding/activation`; the Next.js server
 * forwards to the Go portal API which holds the activation buffer + emits
 * the structured log line. This keeps CORS off the table during local-dev.
 */
const API_BASE = process.env.FDH_API_BASE_URL ?? "http://localhost:8080";

export async function POST(request: NextRequest) {
  try {
    const body = await request.json();
    const upstream = await fetch(`${API_BASE}/api/v1/activation`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    return NextResponse.json(
      await upstream.json().catch(() => ({})),
      { status: upstream.status }
    );
  } catch (err) {
    return NextResponse.json(
      { error: "forward_failed", message: String(err) },
      { status: 502 }
    );
  }
}
