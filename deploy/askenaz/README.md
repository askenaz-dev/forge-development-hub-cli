# Deploying fdh-portal to the askenaz.dev cluster

This directory holds the deployment artifacts specific to the askenaz.dev installation: production Helm values, the role-map ConfigMap, and idempotent provisioning scripts for Keycloak + Kubernetes.

Everything here is intended to be **re-runnable**: every script checks for existing resources before creating. Re-running fixes drift; running on a fresh cluster bootstraps from zero.

```
deploy/askenaz/
├── README.md             ← you are here
├── values-askenaz.yaml   ← Helm values overrides for prod (no secrets)
├── role-map.yaml         ← Keycloak group → portal role mapping
├── setup-keycloak.ps1    ← provision realm + client + groups in Keycloak
└── setup-k8s.ps1         ← create namespace + secrets + configmap in K8s
```

## Prerequisites

- PowerShell 7+ (the scripts use `Invoke-RestMethod` with `-SkipHttpErrorCheck`)
- `kubectl` + `helm` on PATH (already installed)
- Cloudflare Origin Certificate for `*.askenaz.dev` (PEM + key files)
- Keycloak admin credentials for `keycloak.askenaz.dev`
- Kubeconfig pointed at the askenaz cluster (`kubectl config current-context`)
- The Docker images `ghcr.io/askenaz-dev/fdh-portal-{api,web}` published — produced by `.github/workflows/docker-publish.yml` after the rebrand-askenaz PR merges to `main`

## Deploy from zero — 5 steps

```powershell
# --- Step 1: Provision Keycloak realm + client + groups ---------------
$env:KC_ADMIN_USER     = 'admin'
$env:KC_ADMIN_PASSWORD = '<your-admin-password>'
.\setup-keycloak.ps1
# Note the printed client_secret — you need it for the next step.

# --- Step 2: Generate the Cloudflare Origin Certificate ---------------
# 1. Cloudflare dashboard → SSL/TLS → Origin Server → "Create Certificate"
# 2. Hostnames: *.askenaz.dev, askenaz.dev   (wildcard + apex)
# 3. Key type: RSA 2048 (or ECC P-256 if you prefer)
# 4. Validity: 15 years
# 5. Save the cert as origin.pem and the key as origin.key

# --- Step 3: Create K8s namespace + secrets + configmap --------------
$env:FDH_OIDC_CLIENT_SECRET = '<printed-by-step-1>'
$env:FDH_TLS_CERT_PATH      = 'C:\path\to\origin.pem'
$env:FDH_TLS_KEY_PATH       = 'C:\path\to\origin.key'
.\setup-k8s.ps1

# --- Step 4: Install the chart ----------------------------------------
helm upgrade --install fdh-portal ../helm/fdh-portal `
  --namespace fdh `
  -f .\values-askenaz.yaml

# --- Step 5: Point Cloudflare DNS at the ingress ----------------------
# In Cloudflare → askenaz.dev → DNS:
#   Add A or CNAME record:
#     Name:    fdh
#     Content: <your nginx ingress controller external IP / hostname>
#     Proxy:   Proxied (orange cloud)  ← required for Origin Cert to work
# Cloudflare TLS mode must be "Full (strict)" (Settings → SSL/TLS)
```

## Verify

```powershell
# Pods up?
kubectl -n fdh get pods

# API serving the catalog?
kubectl -n fdh port-forward svc/fdh-portal-api 18080:8080
# In another shell:
curl -s http://localhost:18080/api/v1/skills | jq '.items | length'

# Open the portal:
start https://fdh.askenaz.dev
```

You should see the landing page, "Sign in" redirects to Keycloak, after auth you land back on `/profile` with your groups visible.

## Updating

To roll a new image tag without touching everything else:

```powershell
helm upgrade fdh-portal ../helm/fdh-portal `
  --namespace fdh `
  -f .\values-askenaz.yaml `
  --set api.image.tag=v0.2.0 `
  --set web.image.tag=v0.2.0
```

## Rotation

| Thing to rotate                  | Command                                                                       |
|----------------------------------|-------------------------------------------------------------------------------|
| Keycloak admin password          | Change in Keycloak UI, update `$env:KC_ADMIN_PASSWORD` for next run.          |
| Keycloak client secret           | Rerun `setup-keycloak.ps1` (it prints the current secret), then `setup-k8s.ps1` to patch the K8s secret. Restart pods. |
| Cloudflare Origin Cert (15-year) | Set new `$env:FDH_TLS_*_PATH` → rerun `setup-k8s.ps1`. nginx picks up via secret watcher.   |
| Auth.js auth_secret              | Delete the K8s secret + rerun `setup-k8s.ps1`. All active sessions invalidate. |

## Troubleshooting

| Symptom                                  | Likely cause                                                | Fix                                                  |
|------------------------------------------|-------------------------------------------------------------|------------------------------------------------------|
| `setup-keycloak.ps1` → HTTP 523          | Origin server unreachable through Cloudflare.               | Check that Keycloak pod is running and exposed.      |
| `helm upgrade` → ImagePullBackOff        | GHCR images don't exist yet (PR not merged + workflow not run). | Wait for `docker-publish.yml` to run on `main`.   |
| Sign-in loops back to landing            | Role-map drift or missing `groups` claim.                   | `kubectl logs -n fdh deploy/fdh-portal-api` for the trace; check the protocol mapper exists in Keycloak. |
| Browser shows TLS error                  | Cloudflare in "Full" instead of "Full (strict)".            | Cloudflare → SSL/TLS → set to "Full (strict)".       |
| `/readyz` returns 503                    | API can't clone the registry repo.                          | Check `portal.registryURL` value + that the K8s nodes can reach GitHub. |
