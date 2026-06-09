import { NextRequest, NextResponse } from "next/server";
import { editHarness, type HarnessEditPayload } from "@/lib/api";
import {
  gateAndAuthorize,
  gitopsResultResponse,
  forwardFailed,
} from "../_shared";

/**
 * BFF forwarder — POST /api/gitops/harness (capability portal-gitops-write).
 *
 * Role-gated to `publisher`+ BEFORE forwarding. On pass, mints the service
 * credential and calls the Go harness endpoint with the trusted session identity
 * as attribution metadata. The bot opens ONE PR touching only
 * hub/harnesses.yaml; an ADDED component not in the catalog is rejected (422)
 * before composing. The bot cannot merge.
 */
export async function POST(request: NextRequest) {
  const gate = await gateAndAuthorize("harness");
  if (!gate.ok) return gate.response;

  let body: Partial<HarnessEditPayload>;
  try {
    body = (await request.json()) as Partial<HarnessEditPayload>;
  } catch {
    return NextResponse.json(
      { error: "bad_request", message: "invalid JSON body" },
      { status: 400 }
    );
  }
  if (!body || typeof body.harness !== "string" || !body.harness) {
    return NextResponse.json(
      { error: "bad_request", message: "harness name is required" },
      { status: 400 }
    );
  }

  // Forward only the contract fields; arrays of component names per kind.
  const strArr = (v: unknown): string[] | undefined =>
    Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : undefined;
  const payload: HarnessEditPayload = {
    harness: body.harness,
    description: typeof body.description === "string" ? body.description : undefined,
    owner_team: typeof body.owner_team === "string" ? body.owner_team : undefined,
    add_skills: strArr(body.add_skills),
    remove_skills: strArr(body.remove_skills),
    add_rules: strArr(body.add_rules),
    remove_rules: strArr(body.remove_rules),
    add_agents: strArr(body.add_agents),
    remove_agents: strArr(body.remove_agents),
    add_hooks: strArr(body.add_hooks),
    remove_hooks: strArr(body.remove_hooks),
  };

  try {
    const result = await editHarness(payload, {
      serviceToken: gate.serviceToken,
      requestor: gate.requestor,
    });
    return gitopsResultResponse(result);
  } catch (err) {
    return forwardFailed(err);
  }
}
