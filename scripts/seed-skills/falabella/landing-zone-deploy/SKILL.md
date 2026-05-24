---
name: landing-zone-deploy
description: Stand up a new service on forge's landing zone — five GitLab repos covering infrastructure (Terraform), GitOps (Flux), IAM, networking, and secrets (Vault). Use when bootstrapping a new service from scratch or migrating an existing one onto the landing zone.
license: MIT
metadata:
  author: platform-engineering
  sdlc_phase: cicd
  landing_zone_repos:
    infrastructure: https://gitlab.forge.internal/landing-zone/infrastructure
    gitops:         https://gitlab.forge.internal/landing-zone/gitops
    iam:            https://gitlab.forge.internal/landing-zone/iam
    networking:     https://gitlab.forge.internal/landing-zone/networking
    secrets:        https://gitlab.forge.internal/landing-zone/vault-config
  related_skills:
    - architecture/forge-architecture-patterns
    - security/devsecops
    - operations/runbook-template
---

# Deploy on the forge landing zone

The landing zone is five GitLab repos that, together, take a new service
from "I have a container image" to "I have a publicly accessible,
authenticated, observable, secret-aware production deployment." This
skill walks through the standard flow.

## The five repos

| Repo | Owns | When you touch it |
|---|---|---|
| `landing-zone/infrastructure` | AWS / GCP base infra via Terraform — VPCs, EKS/GKE clusters, regional resources, shared databases. | Rarely. Platform team owns it. You request changes via merge request. |
| `landing-zone/gitops` | Flux Kustomizations that reconcile every service's Kubernetes manifests in every environment. Source of truth for what's deployed where. | Every new service. Every prod release. |
| `landing-zone/iam` | OIDC providers, Kubernetes ServiceAccount ↔ cloud-IAM bindings, role definitions. | Once per new service to define its runtime role. Again when permissions change. |
| `landing-zone/networking` | Subnet allocations, security groups / NetworkPolicies, ingress routes, DNS, certificates. | Once per new service. Again when ingress changes. |
| `landing-zone/vault-config` | Vault namespaces, paths, policies, and the Kubernetes-auth role bindings that let pods read their secrets. | Once per new service to declare its secret paths. Again when secret needs change. |

## The standard new-service flow

The flow is deliberately linear. Each step gates the next. The total
elapsed time is typically a few days from start to first production
deploy (most of it review wait time, not work).

### 1. Open a change in the OpenSpec hub

Before any repo, draft the change in the hub via `/opsx:propose`. The
proposal names the service, its data class (public / internal / PII /
payment), its expected traffic profile, and the teams it integrates
with. AppSec and architecture review here, not after deployment.

### 2. IAM — declare your runtime role

In `landing-zone/iam`, add a merge request that defines:

- the Kubernetes namespace your service runs in,
- the ServiceAccount it uses,
- the cloud-IAM role the ServiceAccount maps to (via OIDC federation —
  no static AWS keys, no GCP service-account JSON files),
- the minimum set of cloud permissions the role grants.

CI verifies your role's permissions against the platform's deny-list
(no `*:*`, no `iam:*`, no cross-account assumptions outside the
allowed boundary). The MR is reviewed by the platform team.

### 3. Networking — claim your subnet and ingress

In `landing-zone/networking`, add:

- the subnet your service occupies (per region, per environment),
- the NetworkPolicy that declares its allowed ingress and egress
  destinations,
- the Ingress + DNS record(s) under `*.forge.internal` or
  `*.forge.com`,
- the ACM / Cert Manager certificate request.

The networking team reviews and merges. Allocation is tracked in the
repo so two services never collide.

### 4. Secrets — declare your Vault paths and policies

In `landing-zone/vault-config`, add a Terraform module call that
provisions:

- a Vault namespace for your service (`<env>/<service>`),
- the policies that grant `read` on the paths your service needs,
- a Kubernetes-auth role that binds those policies to your service's
  ServiceAccount from step 2.

CI verifies that your policy paths are scoped to your namespace (no
`*` paths, no cross-service reads).

### 5. GitOps — write the manifests

In `landing-zone/gitops`, under `apps/<environment>/<service>/`, add:

- the Kubernetes manifests (Deployment, Service, HPA, ServiceMonitor,
  NetworkPolicy reference, Ingress reference) OR
- a Flux `HelmRelease` pointing at the chart in your service repo.

Flux reconciles your manifests every few minutes. Merging to `main` IS
the deploy. There is no separate `kubectl apply` step.

### 6. Infrastructure (only if needed)

If your service needs new shared infrastructure — a new RDS database,
an S3 bucket, an SQS queue — open a Terraform MR in
`landing-zone/infrastructure`. Most services do NOT need this step:
the landing zone already provides shared Postgres, shared Kafka, and
shared object storage. Use them.

## The promotion ladder

Environments are `dev` → `staging` → `prod`. Promotion is a Flux
configuration change, not a CI deploy. Tools:

- **Image tag**: your service's CI builds a single image, tags it with
  the commit SHA, and signs it with Cosign.
- **dev**: Flux watches the `main` branch of `landing-zone/gitops`. Your
  PR merges → dev redeploys automatically.
- **staging**: Flux watches the `staging` branch. Cut a release PR that
  rebases staging onto main; the platform release manager merges it on a
  schedule.
- **prod**: Flux watches the `prod` branch. Same release manager flow,
  with an additional approval from the service owner.

Every Flux Kustomization has `spec.serviceAccountName` set so a stuck
deploy is debuggable via standard Kubernetes RBAC.

## Verification checklist (before announcing your service is live)

- [ ] `/healthz` and `/readyz` return 200 from inside the cluster.
- [ ] `ServiceMonitor` is scraping your `/metrics` endpoint — see
      Prometheus targets.
- [ ] Traces are arriving at the central OTel collector.
- [ ] Structured logs land in the central log store with the standard
      fields (`service`, `env`, `trace_id`, `user_id`).
- [ ] The runbook is published (`operations/runbook-template`).
- [ ] AppSec has signed off on the threat model.
- [ ] The on-call rotation includes the service.

## Rollback

Rolling back is reverting the manifest change in `landing-zone/gitops`.
Flux reconciles back to the previous state in minutes. Image rollback
specifically is changing the image tag back to the previous SHA. The
old image must still exist in the registry — image retention policies
keep N=20 versions by default; adjust per service if you need longer.

If a rollback fails because the new code wrote incompatible data, your
threat model already noted the data class and the migration plan should
have covered the rollback. If not, that's a postmortem item.

## Anti-patterns

- **`kubectl apply` against a cluster directly.** Bypasses Flux,
  desynchronises gitops, and leaves no audit trail. Reverted on the next
  reconciliation.
- **A monorepo with all the landing-zone bits inside the service repo.**
  Defeats the per-repo review boundaries (networking has a different
  reviewer than IAM than gitops).
- **One MR that touches three landing-zone repos.** Each repo is a
  separate MR, each with its own review. Linking them in the OpenSpec
  change is enough coordination.
- **Bypassing the Vault path convention.** "We'll just use an env var"
  ages badly. Every shortcut here becomes a Friday-night incident later.

## When the landing zone needs to change

Open a change against the relevant repo with the SDD flow. The platform
team treats the landing zone like any other product: pattern bugs are
filed as issues, new patterns are designed as ADRs first.
