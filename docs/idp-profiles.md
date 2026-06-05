# Identity provider profiles — `local` vs `external`

The Forge portal authenticates via **standard OIDC**. *Which* identity provider it
uses is a **deployment choice, not a code change.** Two profiles are first-class:

| Profile | What | When |
|---|---|---|
| `local` | Self-hosted, in-cluster OIDC (e.g. Keycloak) | Homelab / standalone; no external IdP available |
| `external` | A managed OIDC provider (Entra ID, Okta, Auth0, Google, …) | The org already runs SSO |

Two guarantees hold for **both** profiles:

- **No provider-specific coupling.** The portal only ever calls standard OIDC
  endpoints (discovery, JWKS, authorize, token, logout). Any conforming OIDC
  provider works via configuration alone.
- **The catalog never depends on the IdP.** The portal tolerates the IdP being
  unreachable: anonymous browsing/installing stays up, and only authenticated
  actions degrade (a token request during an outage gets a retryable `503
  auth_unavailable`, never a crash). See `portal-runtime-resilience`.

## Configuration surface

| `values.yaml` | env (API) | Meaning |
|---|---|---|
| `oidc.profile` | `FDH_PORTAL_IDP_PROFILE` | `local` or `external` — **informational**, logged at startup |
| `oidc.discoveryURL` | `OIDC_DISCOVERY_URL` | the IdP issuer's discovery URL |
| `oidc.clientID` | `OIDC_CLIENT_ID` | the audience / client id |
| `oidc.clientSecretRef` | (K8s Secret) | client secret + Auth.js secret |
| `oidc.roleMapConfigMap` | `OIDC_ROLE_MAP_PATH` | claim→role map (YAML) |

The web (NextAuth) reads `AUTH_KEYCLOAK_ISSUER` / `AUTH_KEYCLOAK_ID` /
`AUTH_KEYCLOAK_SECRET`. NextAuth's "keycloak" provider is a **generic OIDC
client** — the name is incidental; it points at whatever issuer the active
profile selects.

### `local` profile

- `oidc.profile: "local"`
- `oidc.discoveryURL`: the in-cluster Keycloak realm, e.g.
  `https://keycloak.askenaz.dev/realms/askenaz`.
- **Operating a reliable self-hosted IdP** (persistent datastore, realm-as-code,
  backups) is the **operator's responsibility in their own infra** — the portal
  and this chart do not manage Keycloak (see the chart's "What's NOT in this
  chart"). If that operational burden is unwanted, prefer `external`.

### `external` profile (runbook)

1. Register an OIDC application/client in the managed IdP. Set the redirect URI to
   `https://<host>/api/auth/callback/keycloak`.
2. `oidc.profile: "external"`.
3. `oidc.discoveryURL`: the managed IdP's discovery URL (table below).
4. `oidc.clientID`: the registered client id.
5. Put the client secret + an Auth.js secret in the `fdh-portal-oidc` Secret:
   ```sh
   kubectl -n fdh create secret generic fdh-portal-oidc \
     --from-literal=client_secret=<from-idp> \
     --from-literal=auth_secret=$(openssl rand -hex 32)
   ```
6. Update the role-map ConfigMap (`OIDC_ROLE_MAP_PATH`) to map the provider's
   group/role claim → portal roles (next section).
7. Roll it out (config only — no rebuild):
   ```sh
   helm upgrade fdh-portal ./deploy/helm/fdh-portal -n fdh --reuse-values \
     --set oidc.profile=external \
     --set oidc.discoveryURL=<issuer-discovery-url> \
     --set oidc.clientID=<client-id>
   ```

## Claim → role mapping by provider

Portal roles: `anonymous < consumer < author < reviewer < publisher < admin`.
The role map (`OIDC_ROLE_MAP_PATH`, a YAML `{claim, map}`) maps a claim's values
to roles; the highest-precedence match wins; an authenticated user with no match
defaults to `consumer`.

| Provider | Discovery URL | Group/role claim | Notes |
|---|---|---|---|
| **Keycloak** | `https://<host>/realms/<realm>` | `groups` (or `realm_access.roles`) | Add a "groups" mapper to the client scope. |
| **Microsoft Entra ID** | `https://login.microsoftonline.com/<tenant>/v2.0` | `roles` (App Roles) or `groups` (object ids) | Prefer **App Roles** for stable values; raw group ids need a directory lookup. |
| **Okta** | `https://<org>.okta.com/oauth2/<as>/.well-known/openid-configuration` | `groups` | Add a groups claim to the authorization server + scope. |
| **Auth0** | `https://<tenant>.auth0.com/` | a **namespaced** claim, e.g. `https://forge/roles` | Auth0 requires namespaced custom claims, added via an Action. |
| **Google Workspace** | `https://accounts.google.com` | — (no groups in the ID token) | Groups need the Admin SDK / Cloud Identity; for coarse roles map by `hd` (domain) or email. |

Example role map (`groups` claim, Keycloak/Entra):

```yaml
claim: groups
map:
  fdh-admins: admin
  fdh-publishers: publisher
  fdh-authors: author
```

## Choosing a profile

The strategic decision is captured in the OpenSpec change **`idp-auth-strategy`**.
In short:

- **Have a corporate IdP (Entra/Okta)?** Use `external` — zero self-hosted ops,
  drop-in OIDC. Recommended default when available.
- **Homelab / standalone?** `local` Keycloak works, but its reliability is yours
  to own.
- **Want the lightest fit for a GitHub-centric platform?** GitHub OAuth + team
  membership for authz is a strong option, tracked as a follow-up (it needs a
  small API-side token adapter, since GitHub tokens are not OIDC JWTs).

Whatever you choose, the anonymous catalog is never affected by the IdP.
