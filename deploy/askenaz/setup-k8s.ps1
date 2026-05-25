<#
.SYNOPSIS
    Creates the Kubernetes namespace, secrets, and configmap that the
    fdh-portal Helm chart depends on. Idempotent.

.DESCRIPTION
    Reads sensitive values from env vars (NEVER committed). Expects:
      $env:FDH_OIDC_CLIENT_SECRET — from Keycloak (setup-keycloak.ps1 prints it)
      $env:FDH_TLS_CERT_PATH      — path to the Cloudflare Origin Certificate (.pem)
      $env:FDH_TLS_KEY_PATH       — path to the matching private key (.key)

    Produces:
      Namespace          fdh
      Secret             fdh-portal-oidc      (client_secret + auth_secret)
      Secret (tls)       cloudflare-origin-tls (cert + key)
      ConfigMap          fdh-portal-role-map  (from role-map.yaml)

    auth_secret is auto-generated (32 random bytes hex) and only needs to
    stay stable across restarts to avoid invalidating active Auth.js
    sessions; this script generates it on first run and reuses it on
    subsequent runs.

.PARAMETER Namespace
    Kubernetes namespace. Default: fdh

.PARAMETER KubeContext
    Optional kubectl context override.
#>

[CmdletBinding()]
param(
    [string]$Namespace = 'fdh',
    [string]$KubeContext = '',
    [string]$RoleMapPath = (Join-Path $PSScriptRoot 'role-map.yaml')
)

$ErrorActionPreference = 'Stop'

# --- preflight -----------------------------------------------------------
if (-not (Get-Command kubectl -ErrorAction SilentlyContinue)) {
    Write-Error "kubectl not found on PATH"
    exit 1
}

if (-not $env:FDH_OIDC_CLIENT_SECRET) {
    Write-Error "Set `$env:FDH_OIDC_CLIENT_SECRET (from setup-keycloak.ps1 output)."
    exit 2
}
if (-not $env:FDH_TLS_CERT_PATH -or -not $env:FDH_TLS_KEY_PATH) {
    Write-Error "Set `$env:FDH_TLS_CERT_PATH and `$env:FDH_TLS_KEY_PATH (Cloudflare Origin Certificate)."
    exit 2
}
if (-not (Test-Path $env:FDH_TLS_CERT_PATH)) { throw "cert not found: $env:FDH_TLS_CERT_PATH" }
if (-not (Test-Path $env:FDH_TLS_KEY_PATH))  { throw "key not found:  $env:FDH_TLS_KEY_PATH" }
if (-not (Test-Path $RoleMapPath))           { throw "role-map.yaml not found: $RoleMapPath" }

$ctxArg = if ($KubeContext) { @('--context', $KubeContext) } else { @() }

function Invoke-Kubectl {
    param([string[]]$Args)
    $allArgs = @($ctxArg + $Args)
    & kubectl @allArgs
    if ($LASTEXITCODE -ne 0) { throw "kubectl exited with $LASTEXITCODE: $($Args -join ' ')" }
}

function Test-K8sResource {
    param([string]$Kind, [string]$Name)
    $allArgs = @($ctxArg + 'get', $Kind, $Name, '-n', $Namespace, '--ignore-not-found', '-o', 'name')
    $out = & kubectl @allArgs 2>$null
    return [bool]$out
}

# --- 1. Namespace --------------------------------------------------------
Write-Host "→ Ensuring namespace '$Namespace' exists ..."
$nsArgs = @($ctxArg + 'get', 'namespace', $Namespace, '--ignore-not-found', '-o', 'name')
$nsExists = & kubectl @nsArgs 2>$null
if (-not $nsExists) {
    Invoke-Kubectl @('create', 'namespace', $Namespace)
    Write-Host "+ namespace '$Namespace' created"
} else {
    Write-Host "✓ namespace '$Namespace' already exists"
}

# --- 2. OIDC secret ------------------------------------------------------
Write-Host "`n→ Ensuring secret 'fdh-portal-oidc' exists ..."
if (Test-K8sResource -Kind 'secret' -Name 'fdh-portal-oidc') {
    Write-Host "✓ secret 'fdh-portal-oidc' already exists (rotating client_secret in-place)"
    # Update the client_secret key without touching auth_secret (preserves sessions).
    $patch = @{
        data = @{
            client_secret = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($env:FDH_OIDC_CLIENT_SECRET))
        }
    } | ConvertTo-Json -Depth 5 -Compress
    Invoke-Kubectl @('patch', 'secret', 'fdh-portal-oidc', '-n', $Namespace, '--type=merge', '-p', $patch)
} else {
    # Generate a fresh 32-byte hex auth_secret for Auth.js.
    $bytes = New-Object byte[] 32
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
    $authSecret = ($bytes | ForEach-Object { $_.ToString('x2') }) -join ''
    Invoke-Kubectl @(
        'create', 'secret', 'generic', 'fdh-portal-oidc',
        '-n', $Namespace,
        '--from-literal', "client_secret=$env:FDH_OIDC_CLIENT_SECRET",
        '--from-literal', "auth_secret=$authSecret"
    )
    Write-Host "+ secret 'fdh-portal-oidc' created with fresh auth_secret"
}

# --- 3. TLS secret -------------------------------------------------------
Write-Host "`n→ Ensuring TLS secret 'cloudflare-origin-tls' exists ..."
if (Test-K8sResource -Kind 'secret' -Name 'cloudflare-origin-tls') {
    Write-Host "✓ TLS secret already exists (replacing to refresh cert/key)"
    Invoke-Kubectl @('delete', 'secret', 'cloudflare-origin-tls', '-n', $Namespace)
}
Invoke-Kubectl @(
    'create', 'secret', 'tls', 'cloudflare-origin-tls',
    '-n', $Namespace,
    "--cert=$env:FDH_TLS_CERT_PATH",
    "--key=$env:FDH_TLS_KEY_PATH"
)
Write-Host "+ TLS secret created from $env:FDH_TLS_CERT_PATH"

# --- 4. role-map ConfigMap ----------------------------------------------
Write-Host "`n→ Ensuring configmap 'fdh-portal-role-map' exists ..."
if (Test-K8sResource -Kind 'configmap' -Name 'fdh-portal-role-map') {
    Invoke-Kubectl @('delete', 'configmap', 'fdh-portal-role-map', '-n', $Namespace)
}
Invoke-Kubectl @(
    'create', 'configmap', 'fdh-portal-role-map',
    '-n', $Namespace,
    "--from-file=role-map.yaml=$RoleMapPath"
)
Write-Host "+ configmap created from $RoleMapPath"

Write-Host "`n========================================================="
Write-Host " K8s prerequisites ready in namespace '$Namespace'."
Write-Host " Now deploy the chart:"
Write-Host "   helm upgrade --install fdh-portal ./deploy/helm/fdh-portal ``"
Write-Host "     --namespace $Namespace ``"
Write-Host "     -f ./deploy/askenaz/values-askenaz.yaml"
Write-Host "========================================================="
