<#
.SYNOPSIS
    Provisions the Keycloak realm + client + groups for the fdh-portal.

.DESCRIPTION
    Idempotent: re-running is safe. Reads Keycloak admin credentials from
    the KC_ADMIN_USER / KC_ADMIN_PASSWORD env vars (NEVER commit these).

    What it does:
      1. Authenticates against the Keycloak master realm via password grant.
      2. Creates realm `askenaz` if it doesn't exist.
      3. Creates 4 groups: fdh-admins, fdh-publishers, fdh-reviewers, fdh-authors.
      4. Creates OIDC client `fdh-portal` (confidential, PKCE, standard flow).
      5. Adds a group-membership protocol mapper to the client.
      6. Prints the client_secret to stdout so you can pipe it into the
         Kubernetes Secret created by setup-k8s.ps1.

.PARAMETER KeycloakBase
    Base URL of the Keycloak instance. Default: https://keycloak.askenaz.dev

.PARAMETER Realm
    Realm name to create / use. Default: askenaz

.PARAMETER ClientID
    OIDC client ID to create. Default: fdh-portal

.PARAMETER RedirectURI
    Valid redirect URI for the portal. Default: https://fdh.askenaz.dev/api/auth/callback/keycloak

.EXAMPLE
    $env:KC_ADMIN_USER='admin'
    $env:KC_ADMIN_PASSWORD='<your-password>'
    .\setup-keycloak.ps1
#>

[CmdletBinding()]
param(
    [string]$KeycloakBase = 'https://keycloak.askenaz.dev',
    [string]$Realm        = 'askenaz',
    [string]$ClientID     = 'fdh-portal',
    [string]$RedirectURI  = 'https://fdh.askenaz.dev/api/auth/callback/keycloak',
    [string[]]$Groups     = @('fdh-admins', 'fdh-publishers', 'fdh-reviewers', 'fdh-authors')
)

$ErrorActionPreference = 'Stop'

if (-not $env:KC_ADMIN_USER -or -not $env:KC_ADMIN_PASSWORD) {
    Write-Error "Set `$env:KC_ADMIN_USER and `$env:KC_ADMIN_PASSWORD before running."
    exit 2
}

function Get-AdminToken {
    Write-Host "→ Authenticating against $KeycloakBase/realms/master ..."
    $body = @{
        grant_type = 'password'
        client_id  = 'admin-cli'
        username   = $env:KC_ADMIN_USER
        password   = $env:KC_ADMIN_PASSWORD
    }
    $resp = Invoke-RestMethod -Method Post `
        -Uri "$KeycloakBase/realms/master/protocol/openid-connect/token" `
        -ContentType 'application/x-www-form-urlencoded' `
        -Body $body
    return $resp.access_token
}

function Invoke-KCApi {
    param(
        [Parameter(Mandatory)] [string]$Token,
        [Parameter(Mandatory)] [ValidateSet('GET','POST','PUT','DELETE')] [string]$Method,
        [Parameter(Mandatory)] [string]$Path,
        $Body = $null,
        [int[]]$AcceptableStatus = @(200, 201, 204)
    )
    $url = "$KeycloakBase/admin$Path"
    $headers = @{ Authorization = "Bearer $Token" }
    try {
        if ($Body -ne $null) {
            $json = $Body | ConvertTo-Json -Depth 10
            return Invoke-RestMethod -Method $Method -Uri $url -Headers $headers `
                -ContentType 'application/json' -Body $json
        } else {
            return Invoke-RestMethod -Method $Method -Uri $url -Headers $headers
        }
    } catch {
        $status = $_.Exception.Response.StatusCode.value__
        if ($status -in $AcceptableStatus -or $status -eq 409) {
            return $null  # already exists, fine
        }
        throw "Keycloak $Method $Path -> HTTP $status : $($_.Exception.Message)"
    }
}

# --- 1. Get admin token --------------------------------------------------
$token = Get-AdminToken
Write-Host "✓ admin token acquired (length=$($token.Length))"

# --- 2. Create realm -----------------------------------------------------
Write-Host "`n→ Ensuring realm '$Realm' exists ..."
$existing = $null
try {
    $existing = Invoke-KCApi -Token $token -Method GET -Path "/realms/$Realm"
} catch {
    # not found -> 404 -> we create below
}
if ($existing) {
    Write-Host "✓ realm '$Realm' already exists"
} else {
    Invoke-KCApi -Token $token -Method POST -Path "/realms" -Body @{
        realm                  = $Realm
        enabled                = $true
        registrationAllowed    = $false
        loginTheme             = 'keycloak'
        accessTokenLifespan    = 1800
        ssoSessionIdleTimeout  = 1800
        ssoSessionMaxLifespan  = 36000
    }
    Write-Host "✓ realm '$Realm' created"
}

# --- 3. Create groups ----------------------------------------------------
Write-Host "`n→ Ensuring $($Groups.Count) groups exist ..."
$existingGroups = Invoke-KCApi -Token $token -Method GET -Path "/realms/$Realm/groups"
$existingNames = @($existingGroups | ForEach-Object { $_.name })
foreach ($g in $Groups) {
    if ($existingNames -contains $g) {
        Write-Host "  ✓ group '$g' already exists"
    } else {
        Invoke-KCApi -Token $token -Method POST -Path "/realms/$Realm/groups" -Body @{ name = $g }
        Write-Host "  + group '$g' created"
    }
}

# --- 4. Create client ----------------------------------------------------
Write-Host "`n→ Ensuring client '$ClientID' exists ..."
$clients = Invoke-KCApi -Token $token -Method GET -Path "/realms/$Realm/clients?clientId=$ClientID"
$client = $clients | Where-Object { $_.clientId -eq $ClientID } | Select-Object -First 1

if ($client) {
    Write-Host "✓ client '$ClientID' already exists (id=$($client.id))"
} else {
    Invoke-KCApi -Token $token -Method POST -Path "/realms/$Realm/clients" -Body @{
        clientId                  = $ClientID
        enabled                   = $true
        publicClient              = $false
        protocol                  = 'openid-connect'
        standardFlowEnabled       = $true
        implicitFlowEnabled       = $false
        directAccessGrantsEnabled = $false
        serviceAccountsEnabled    = $false
        redirectUris              = @($RedirectURI)
        webOrigins                = @('https://fdh.askenaz.dev')
        attributes                = @{
            'pkce.code.challenge.method' = 'S256'
        }
    }
    Write-Host "+ client '$ClientID' created"
    $clients = Invoke-KCApi -Token $token -Method GET -Path "/realms/$Realm/clients?clientId=$ClientID"
    $client  = $clients | Where-Object { $_.clientId -eq $ClientID } | Select-Object -First 1
}

# --- 5. Add group-membership mapper -------------------------------------
Write-Host "`n→ Ensuring 'groups' protocol mapper exists ..."
$mappers = Invoke-KCApi -Token $token -Method GET -Path "/realms/$Realm/clients/$($client.id)/protocol-mappers/models"
$existing = $mappers | Where-Object { $_.name -eq 'groups' } | Select-Object -First 1
if ($existing) {
    Write-Host "✓ mapper 'groups' already exists"
} else {
    Invoke-KCApi -Token $token -Method POST `
        -Path "/realms/$Realm/clients/$($client.id)/protocol-mappers/models" `
        -Body @{
            name           = 'groups'
            protocol       = 'openid-connect'
            protocolMapper = 'oidc-group-membership-mapper'
            config         = @{
                'full.path'           = 'false'
                'id.token.claim'      = 'true'
                'access.token.claim'  = 'true'
                'userinfo.token.claim'= 'true'
                'claim.name'          = 'groups'
            }
        }
    Write-Host "+ mapper 'groups' created"
}

# --- 6. Fetch and print client secret -----------------------------------
Write-Host "`n→ Fetching client secret ..."
$secret = Invoke-KCApi -Token $token -Method GET -Path "/realms/$Realm/clients/$($client.id)/client-secret"
$clientSecret = $secret.value

Write-Host "`n========================================================="
Write-Host " Keycloak provisioning complete."
Write-Host "  Realm:           $Realm"
Write-Host "  Discovery URL:   $KeycloakBase/realms/$Realm/.well-known/openid-configuration"
Write-Host "  Client ID:       $ClientID"
Write-Host "  Redirect URI:    $RedirectURI"
Write-Host "  Groups:          $($Groups -join ', ')"
Write-Host "  Client Secret:   $clientSecret"
Write-Host "========================================================="
Write-Host "`nPipe the client secret into the K8s Secret with:"
Write-Host "  `$env:FDH_OIDC_CLIENT_SECRET = '$clientSecret'"
Write-Host "  .\setup-k8s.ps1"
