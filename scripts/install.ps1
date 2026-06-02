<#
.SYNOPSIS
    Installs the fdh CLI on Windows.

.DESCRIPTION
    Downloads the latest (or requested) `fdh` release for windows/amd64,
    verifies its SHA-256, installs to $env:USERPROFILE\.fdh\bin\fdh.exe,
    and adds that directory to the user-level PATH (HKCU:\Environment).

    arm64 is out of scope for this initial release; the script aborts with
    a clear message if the host is arm64.

.PARAMETER Version
    Specific version to install (e.g. v0.5.2). Defaults to 'latest' which
    is resolved from the GitHub Releases API.

.NOTES
    Env vars honoured:
      FDH_RELEASES_BASE - Base URL where the release assets live. Defaults
                          to the upstream GitHub Releases. Override to point
                          at a private mirror with the same per-tag layout.
      FDH_LATEST_URL    - URL returning JSON with a `tag_name` field for
                          "latest". Defaults to GitHub's API.
      FDH_INSTALL_DIR   - Override the install directory.
                          (default: $env:USERPROFILE\.fdh\bin).

    Exit codes (stable):
      0  success
      1  generic error
      2  invalid usage
      3  unsupported arch
      4  network error
      5  checksum mismatch

    ExecutionPolicy: this script is intended to be invoked via
        iwr https://raw.githubusercontent.com/askenaz-dev/forge-development-hub-cli/main/scripts/install.ps1 | iex
    from a PowerShell session whose policy already permits running remote
    scripts. If yours doesn't, run the equivalent download + execute
    yourself with -ExecutionPolicy Bypass.
#>

[CmdletBinding()]
param(
    [string]$Version = "latest"
)

$ErrorActionPreference = "Stop"
$ProgressPreference   = "SilentlyContinue"

# --- defaults ------------------------------------------------------------

$DefaultReleasesBase = "https://github.com/askenaz-dev/forge-development-hub-cli/releases"
$DefaultLatestUrl    = "https://api.github.com/repos/askenaz-dev/forge-development-hub-cli/releases/latest"

$ReleasesBase = if ($env:FDH_RELEASES_BASE) { $env:FDH_RELEASES_BASE } else { $DefaultReleasesBase }
$LatestUrl    = if ($env:FDH_LATEST_URL)    { $env:FDH_LATEST_URL }    else { $DefaultLatestUrl }
$InstallDir   = if ($env:FDH_INSTALL_DIR)   { $env:FDH_INSTALL_DIR }   else { Join-Path $env:USERPROFILE ".fdh\bin" }

# --- arch detection ------------------------------------------------------

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { "amd64" }
    "ARM64" {
        Write-Error "windows/arm64 is not supported by this installer (initial release ships amd64 only)."
        exit 3
    }
    default {
        Write-Error "unsupported PROCESSOR_ARCHITECTURE: $($env:PROCESSOR_ARCHITECTURE)"
        exit 3
    }
}
$target = "windows-$arch"

# --- resolve version -----------------------------------------------------

function Resolve-Version {
    param([string]$Requested)

    if ($Requested -ne "latest") {
        return $Requested
    }
    try {
        $release = Invoke-RestMethod -Uri $LatestUrl -UseBasicParsing -TimeoutSec 30 `
            -Headers @{ "Accept" = "application/vnd.github+json" }
    } catch {
        Write-Error "could not fetch latest release info at $LatestUrl ($($_.Exception.Message))"
        exit 4
    }
    if (-not $release.tag_name) {
        Write-Error "latest release info at $LatestUrl does not declare a 'tag_name' field"
        exit 4
    }
    return $release.tag_name
}

$Version = Resolve-Version -Requested $Version

# --- artifact URL convention --------------------------------------------

# Release tags are vX.Y.Z and the Releases download path uses the tag
# verbatim; goreleaser strips the leading "v" from the artifact *filename*
# ({{ .Version }} -> "0.2.6"). Normalise the tag to carry a leading "v"
# (covers -Version 0.2.6) and derive the v-less form for the filename.
if ($Version -notmatch '^v') { $Version = "v$Version" }
$versionNoV  = $Version -replace '^v', ''

# Matches the artifact names emitted by .goreleaser.yaml.
$artifact    = "fdh_${versionNoV}_windows_${arch}.zip"
$urlBase     = "$ReleasesBase/download/$Version"
$artifactUrl = "$urlBase/$artifact"
$shaUrl      = "$artifactUrl.sha256"

Write-Host "fdh installer"
Write-Host "  target:    $target"
Write-Host "  version:   $Version"
Write-Host "  releases:  $ReleasesBase"
Write-Host "  install:   $InstallDir"

# --- idempotency check ---------------------------------------------------

$binPath        = Join-Path $InstallDir "fdh.exe"
$markerPath     = Join-Path $InstallDir ".last-tarball-sha256"

$expectedSha = $null
try {
    $expectedSha = (Invoke-WebRequest -Uri $shaUrl -UseBasicParsing -TimeoutSec 30).Content.Trim().Split(" ")[0]
} catch {
    # Non-fatal here — we'll re-error below if we actually need to download.
}

if ((Test-Path $binPath) -and (Test-Path $markerPath) -and $expectedSha) {
    $lastSha = (Get-Content $markerPath -Raw).Trim()
    if ($lastSha -eq $expectedSha) {
        Write-Host "already up-to-date: $binPath"
        exit 0
    }
}

# --- download + verify + extract ----------------------------------------

$tmpDir = New-Item -ItemType Directory -Path (Join-Path $env:TEMP "fdh-install-$([Guid]::NewGuid().ToString('N'))")
try {
    $zipPath = Join-Path $tmpDir.FullName $artifact
    $shaPath = "$zipPath.sha256"

    Write-Host "downloading $artifactUrl"
    try {
        Invoke-WebRequest -Uri $artifactUrl -OutFile $zipPath -UseBasicParsing -TimeoutSec 120
    } catch {
        Write-Error "download failed: $artifactUrl ($($_.Exception.Message))"
        exit 4
    }
    try {
        Invoke-WebRequest -Uri $shaUrl -OutFile $shaPath -UseBasicParsing -TimeoutSec 30
    } catch {
        Write-Error "checksum download failed: $shaUrl ($($_.Exception.Message))"
        exit 4
    }

    $expectedSha = (Get-Content $shaPath -Raw).Trim().Split(" ")[0]
    $actualSha   = (Get-FileHash $zipPath -Algorithm SHA256).Hash.ToLower()
    if ($expectedSha -ne $actualSha) {
        Write-Error "checksum mismatch for $artifact (expected $expectedSha, got $actualSha)"
        exit 5
    }
    Write-Host "checksum ok"

    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    }

    $stage = New-Item -ItemType Directory -Path (Join-Path $tmpDir.FullName "stage")
    Expand-Archive -Path $zipPath -DestinationPath $stage.FullName -Force

    $extracted = Get-ChildItem -Path $stage.FullName -Recurse -Filter "fdh.exe" |
        Select-Object -First 1
    if (-not $extracted) {
        Write-Error "'fdh.exe' not found inside $artifact"
        exit 1
    }
    Copy-Item -Path $extracted.FullName -Destination $binPath -Force
    Set-Content -Path $markerPath -Value $expectedSha -NoNewline
    Write-Host "installed $binPath"
} finally {
    Remove-Item -Recurse -Force $tmpDir.FullName -ErrorAction SilentlyContinue
}

# --- PATH editing --------------------------------------------------------

$envPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (-not $envPath) { $envPath = "" }

$entries = $envPath.Split(";") | Where-Object { $_ -ne "" }
if ($entries -notcontains $InstallDir) {
    $newPath = if ($envPath -eq "") { $InstallDir } else { "$envPath;$InstallDir" }
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    Write-Host "added $InstallDir to user PATH"
    Write-Host "reopen PowerShell (or run `$env:PATH += ';$InstallDir`) so the change takes effect in this shell."
} else {
    Write-Host "$InstallDir already on user PATH"
}

Write-Host ""
Write-Host "All set. Run:  fdh --version"
