import { NextRequest, NextResponse } from "next/server";
import { importComponent, type ImportFormPayload } from "@/lib/api";
import {
  gateAndAuthorize,
  gitopsResultResponse,
  forwardFailed,
} from "../_shared";

/**
 * BFF forwarder — POST /api/gitops/import (capability portal-gitops-write).
 *
 * Role-gated to `author`+ BEFORE forwarding (the advisory UX gate; the Go API
 * re-enforces it authoritatively). On pass, mints the Phase-1 service credential
 * and calls the Go import endpoint with the trusted session identity as
 * attribution metadata. The bot validates the bundle server-side and opens ONE
 * PR adding the component as non-default — it cannot merge.
 *
 * Accepts two content types, mirroring the Go handler:
 *   - multipart/form-data — a zip upload (`bundle` file + kind/name/owner_team/
 *     agents fields). Forwarded as a re-built FormData so the multipart boundary
 *     is regenerated cleanly by the upstream fetch.
 *   - application/json — a skill form (kind/name/description/owner_team/agents/
 *     files).
 */
export async function POST(request: NextRequest) {
  const gate = await gateAndAuthorize("import");
  if (!gate.ok) return gate.response;

  const contentType = request.headers.get("content-type") ?? "";

  try {
    if (contentType.includes("multipart/form-data")) {
      // Re-read the multipart body and re-emit a fresh FormData so the upstream
      // fetch sets its own boundary. We forward only the contract fields.
      const incoming = await request.formData();
      const forward = new FormData();
      for (const field of ["kind", "name", "owner_team", "agents"]) {
        const v = incoming.get(field);
        if (typeof v === "string" && v) forward.set(field, v);
      }
      const bundle = incoming.get("bundle");
      if (!(bundle instanceof Blob)) {
        return NextResponse.json(
          { error: "bad_request", message: "missing 'bundle' zip upload" },
          { status: 400 }
        );
      }
      forward.set("bundle", bundle, fileNameOf(incoming.get("bundle")));

      const result = await importComponent(forward, {
        serviceToken: gate.serviceToken,
        requestor: gate.requestor,
      });
      return gitopsResultResponse(result);
    }

    // JSON skill-form path.
    const body = (await request.json()) as Partial<ImportFormPayload>;
    if (!body || typeof body.name !== "string" || !body.name) {
      return NextResponse.json(
        { error: "bad_request", message: "name is required" },
        { status: 400 }
      );
    }
    const payload: ImportFormPayload = {
      kind: "skill",
      name: body.name,
      description: typeof body.description === "string" ? body.description : undefined,
      owner_team: typeof body.owner_team === "string" ? body.owner_team : undefined,
      agents: Array.isArray(body.agents) ? body.agents : undefined,
      files:
        body.files && typeof body.files === "object" ? body.files : undefined,
    };

    const result = await importComponent(payload, {
      serviceToken: gate.serviceToken,
      requestor: gate.requestor,
    });
    return gitopsResultResponse(result);
  } catch (err) {
    return forwardFailed(err);
  }
}

/** Recover a filename for the forwarded zip; falls back to a generic name. */
function fileNameOf(v: FormDataEntryValue | null): string {
  if (v instanceof File && v.name) return v.name;
  return "bundle.zip";
}
