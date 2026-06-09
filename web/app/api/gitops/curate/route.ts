import { NextRequest, NextResponse } from "next/server";
import { curate, type CuratePayload } from "@/lib/api";
import {
  gateAndAuthorize,
  gitopsResultResponse,
  forwardFailed,
} from "../_shared";

const KINDS = ["skill", "rule", "agent", "hook"] as const;
const LIFECYCLES = ["deprecate", "yank"] as const;

/**
 * BFF forwarder — POST /api/gitops/curate (capability portal-gitops-write).
 *
 * Role-gated to `admin` BEFORE forwarding. On pass, mints the service credential
 * and calls the Go curate endpoint with the trusted session identity as
 * attribution metadata. The bot opens ONE PR editing hub/registry.yaml (and, for
 * a default flip, the `default` harness atomically). Forward-only lifecycle:
 * un-yank is rejected (422) and no PR is created. The bot cannot merge.
 */
export async function POST(request: NextRequest) {
  const gate = await gateAndAuthorize("curate");
  if (!gate.ok) return gate.response;

  let body: Partial<CuratePayload>;
  try {
    body = (await request.json()) as Partial<CuratePayload>;
  } catch {
    return NextResponse.json(
      { error: "bad_request", message: "invalid JSON body" },
      { status: 400 }
    );
  }
  if (
    !body ||
    typeof body.kind !== "string" ||
    !KINDS.includes(body.kind as (typeof KINDS)[number]) ||
    typeof body.name !== "string" ||
    !body.name
  ) {
    return NextResponse.json(
      { error: "bad_request", message: "kind and name are required" },
      { status: 400 }
    );
  }

  const payload: CuratePayload = {
    kind: body.kind as CuratePayload["kind"],
    name: body.name,
  };
  if (typeof body.set_default === "boolean") payload.set_default = body.set_default;
  if (
    typeof body.lifecycle === "string" &&
    LIFECYCLES.includes(body.lifecycle as (typeof LIFECYCLES)[number])
  ) {
    payload.lifecycle = body.lifecycle as CuratePayload["lifecycle"];
  }
  if (typeof body.version === "string" && body.version) payload.version = body.version;

  try {
    const result = await curate(payload, {
      serviceToken: gate.serviceToken,
      requestor: gate.requestor,
    });
    return gitopsResultResponse(result);
  } catch (err) {
    return forwardFailed(err);
  }
}
