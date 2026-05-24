---
name: devsecops
description: Security review + DevSecOps practices for forge services. Covers threat modeling, SAST/DAST gates, dependency + container scanning, secret management via Vault, runtime security, and the shift-left checklist every PR runs through. Use when reviewing security posture, wiring security into a pipeline, or onboarding a new service to the platform.
license: MIT
metadata:
  author: appsec
  sdlc_phase: security
  related_skills:
    - security/owasp-quick-review
    - cicd/landing-zone-deploy
---

# DevSecOps at forge

The job of security is not gatekeeping; it is making the secure path the
fast path. This skill is the recipe every service follows.

## Shift-left checklist (per PR)

Every pull request must pass these gates before merge. CI enforces all
of them.

| Gate | What it checks | Blocks on |
|---|---|---|
| Secret scan | committed credentials, tokens, private keys | any finding |
| SAST | language-native static analysis (per `forge-tech-stack`) | severity `high` or `critical` |
| Dependency scan | known CVEs in declared dependencies | severity `high` or `critical`, no patched version available |
| License scan | dependency licenses against the approved list | any `forbidden` license |
| Container scan | base image + installed packages CVEs | severity `critical` with active exploitation |
| SonarLint quality gate | code smells, duplication, coverage | `failing` rating |
| Signed commits | every commit signed with the developer's verified key | unsigned commit |

The exact tools per language come from
`forge-tech-stack` (the `code_quality` block). The thresholds above
are platform-wide minima; teams can be stricter, never looser.

## Threat modeling

Every new service runs a STRIDE pass before its first production deploy.
Existing services revisit threat models when:

- a new authn/authz boundary appears,
- a new data class is stored (PII, payment, health, etc.),
- a new external integration is added,
- a major architectural change ships.

STRIDE categories to walk through per trust boundary:

- **S** — Spoofing identity (authn weakness)
- **T** — Tampering with data (integrity weakness)
- **R** — Repudiation (audit weakness)
- **I** — Information disclosure (confidentiality weakness)
- **D** — Denial of service
- **E** — Elevation of privilege (authz weakness)

The output is a threat model document committed to the service repo
under `docs/threat-model.md`. Each identified threat lists the mitigation
or accepted risk. AppSec reviews the document; their review is the gate
to production deployment.

## Secret management

**No secrets in source. No secrets in environment variables baked into
images. No secrets in CI variables outside the secrets manager.**

Secrets live in HashiCorp Vault, accessed via the service's Kubernetes
service account through the Vault Agent Injector. The path convention is
`secret/data/<environment>/<service>/<key>`. See
`cicd/landing-zone-deploy` for the Vault repo (one of the five landing
zone repos) and the per-service onboarding flow.

The forbidden patterns:

- `.env.production` committed to the repo
- secret values in a Kubernetes ConfigMap
- secrets baked into a container image at build time
- secrets passed as CI variables outside the central secrets manager

If you find one in a code review, block the PR and open a security
incident.

## Authentication

- **Inside the platform**: services authenticate to each other via
  mutual TLS (mTLS), with certificates issued by the platform PKI.
  Service-to-service tokens are deprecated.
- **From users**: the platform's Keycloak issues OIDC tokens. Services
  validate the bearer token, check the audience, and read the standard
  claims. Never accept a token whose `aud` doesn't include your service.
- **From partners**: explicit per-partner credentials, rotated on a
  documented schedule, scoped to the minimum operations required.

## Authorization

- **Coarse-grained**: deny by default, allow per resource type + action,
  scoped by tenant or organisation where applicable.
- **Fine-grained**: when a single user can be granted access to specific
  rows or fields, use a policy engine (OPA / Cedar / equivalent —
  consult the architecture guild). Do NOT roll your own.
- **Enforce at every layer**: API gateway alone is not enough — the
  service must verify authz again, because internal callers may
  bypass the gateway.

## Runtime security

- **Pod security**: every workload runs with `runAsNonRoot: true`,
  `readOnlyRootFilesystem: true`, dropped capabilities, no privilege
  escalation. The landing zone gitops configs enforce this.
- **Network policies**: deny by default at the Kubernetes namespace
  level. Allow only the egress and ingress the service actually needs.
- **Image signing + admission**: all production images are signed with
  Cosign. Admission controllers reject unsigned images.
- **Audit logging**: every authentication event, authorization decision,
  and sensitive data access is logged to the central SIEM with the
  standard event shape.

## Incident response

When a security finding is confirmed in production:

1. **Contain** — rotate credentials, revoke tokens, block the affected
   path. Speed matters more than precision in this step.
2. **Notify** — security on-call (PagerDuty rotation `secops-oncall`)
   and the service owner. If user data is involved, the data protection
   officer (DPO) is also paged.
3. **Investigate** — preserve evidence (logs, snapshots, traces) before
   any cleanup that would destroy them.
4. **Remediate** — fix the underlying cause; verify with a focused test
   that exercises the original attack.
5. **Postmortem** — within 5 business days, using the
   `operations/runbook-template` postmortem template, with explicit
   action items and owners.

## Anti-patterns

- **"We'll add security later."** Later never arrives. Add the gates
  in the first PR.
- **Bypassing scans for "just this one PR".** Either the scan rule is
  wrong (fix the rule via a change) or the finding is real (fix the
  finding). There is no third option.
- **Storing tokens in localStorage.** XSS turns into account takeover.
  Use HTTP-only cookies for browsers, never JavaScript-accessible storage.
- **Logging full requests.** Tokens, PII, and payment data don't belong
  in logs. Apply the central log redaction filter.
- **Custom crypto.** If you find yourself writing AES code, stop and
  reach for the platform-provided KMS or libsodium wrapper.

## Where AppSec can be reached

- Slack: `#appsec-help` for general questions, `#secops-incidents` for
  active incidents.
- PagerDuty: `secops-oncall` for 24/7 critical findings.
- Threat-model review queue: open a ticket in the AppSec board with
  the service name and the model doc URL.
