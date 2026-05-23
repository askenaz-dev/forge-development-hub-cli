# Keycloak setup for the FDH portal

The portal authenticates against Falabella's existing Keycloak instance.
This page is the handoff to the platform identity team — one realm
config + one OIDC client + four optional groups for role mapping.

## What we need

### 1. A realm (or reuse an existing one)

The portal does not require a dedicated realm; reusing the company-wide
realm is preferred. The portal does need the realm to expose:

- **groups claim** in the access token and the ID token
- **email** + **profile** scopes (standard)
- **PKCE-only** support enabled (no implicit, no password grant)

### 2. An OIDC client

| Setting              | Value                                                       |
| -------------------- | ----------------------------------------------------------- |
| Client ID            | `fdh-portal`                                                |
| Client type          | Confidential (or public + PKCE — both supported)            |
| Standard flow        | Enabled                                                     |
| Implicit flow        | Disabled                                                    |
| Direct access grants | Disabled                                                    |
| Service accounts     | Disabled (unless future automation requires)                |
| Valid redirect URIs  | `https://fdh.falabella.internal/api/auth/callback/keycloak` |
| Web origins          | `https://fdh.falabella.internal`                            |
| PKCE                 | Required (`S256`)                                           |

### 3. Group claim mapper

Add an `oidc-group-membership-mapper` to the `fdh-portal` client so the
JWT carries a `groups` claim with the user's group memberships.

Mapper settings:

| Setting              | Value         |
| -------------------- | ------------- |
| Mapper type          | Group Membership |
| Token Claim Name     | `groups`      |
| Full group path      | `false`       |
| Add to ID token      | `true`        |
| Add to access token  | `true`        |
| Add to userinfo      | `true`        |

### 4. Groups (or roles — either works)

The portal recognizes four groups by default; create whichever subset
your org actually uses. Membership maps to portal roles via the
`role-map.yaml` ConfigMap.

| Group name        | Portal role |
| ----------------- | ----------- |
| `fdh-admins`      | admin       |
| `fdh-publishers`  | publisher   |
| `fdh-reviewers`   | reviewer    |
| `fdh-authors`     | author      |

Users with no group default to `consumer` after authentication.
Unauthenticated users are `anonymous`.

### 5. Credentials

After the client is created, the platform team shares:

- `client_secret` (for confidential clients)

That goes into a Kubernetes Secret in the portal's namespace; see the
Helm chart README under `deploy/helm/fdh-portal/`.

## What we deliver

In exchange, we configure:

- `oidc.discoveryURL` — `https://<keycloak>/realms/<realm>` (full URL)
- `oidc.clientID` — `fdh-portal`
- `oidc.roleMapConfigMap` — a ConfigMap with the claim→role mapping
- A Kubernetes Secret holding `client_secret` + `auth_secret`

## Local dev parity

The portal's local-dev stack (`docker compose up`) boots a Keycloak
container with a pre-seeded realm export at
[`compose/keycloak/realm-fdh.json`](../compose/keycloak/realm-fdh.json).
The platform team can use that file as a reference for the production
realm.

## Switching IdPs

If Falabella ever moves off Keycloak (e.g. to Entra ID), only configuration
changes — no portal code change. Auth.js's provider list and the Go API's
`go-oidc` verifier both accept any conforming OIDC IdP. Document the
swap in a new OpenSpec change before flipping the discovery URL.
