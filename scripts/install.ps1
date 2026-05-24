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
    is resolved from manifest.json published by the release pipeline.

.NOTES
    Env vars honoured:
      FDH_PKG_HOST   - override the download host
                       (default: pkg.forge.internal — placeholder
                       until the platform team confirms the real host).
      FDH_INSTALL_DIR - override the install directory
                       (default: $env:USERPROFILE\.fdh\bin).

    Exit codes (stable):
      0  success
      1  generic error
      2  invalid usage
      3  unsupported arch
      4  network error
      5  checksum mismatch

    ExecutionPolicy: this script is intended to be invoked via
        iwr https://<host>/fdh/install.ps1 | iex
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

$DefaultHost = "pkg.forge.internal"
$PkgHost     = if ($env:FDH_PKG_HOST) { $env:FDH_PKG_HOST } else { $DefaultHost }
$InstallDir  = if ($env:FDH_INSTALL_DIR) { $env:FDH_INSTALL_DIR } else { Join-Path $env:USERPROFILE ".fdh\bin" }

if ($PkgHost -eq $DefaultHost) {
    Write-Warning "FDH_PKG_HOST not set; using placeholder default '$DefaultHost'."
    Write-Warning "Set `$env:FDH_PKG_HOST to the real forge host before deploying."
}

# --- arch detection (task 8.1) -------------------------------------------

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

# --- resolve version (task 8.3) ------------------------------------------

$manifestUrl = "https://$PkgHost/fdh/manifest.json"

function Resolve-Version {
    param([string]$Requested)

    if ($Requested -ne "latest") {
        return $Requested
    }
    try {
        $manifest = Invoke-RestMethod -Uri $manifestUrl -UseBasicParsing -TimeoutSec 30
    } catch {
        Write-Error "could not fetch manifest at $manifestUrl ($($_.Exception.Message))"
        exit 4
    }
    if (-not $manifest.latest) {
        Write-Error "manifest at $manifestUrl does not declare a 'latest' field"
        exit 4
    }
    return $manifest.latest
}

$Version = Resolve-Version -Requested $Version

# --- artifact URL convention --------------------------------------------

# Mirror of install.sh's URL convention; matches the artifact names
# emitted by .goreleaser.yaml + the manifest publisher.
$artifact   = "fdh_${Version}_windows_${arch}.zip"
$urlBase    = "https://$PkgHost/fdh/$Version"
$artifactUrl = "$urlBase/$artifact"
$shaUrl      = "$artifactUrl.sha256"

Write-Host "fdh installer"
Write-Host "  target:   $target"
Write-Host "  version:  $Version"
Write-Host "  host:     $PkgHost"
Write-Host "  install:  $InstallDir"

# --- idempotency check (task 8.7) ---------------------------------------

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

# --- download + verify + extract (tasks 8.4, 8.5) -----------------------

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

    # Expand into a temp staging dir so we can locate fdh.exe wherever it
    # lives inside the archive (versioned subdir or flat).
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

# --- PATH editing (task 8.6) --------------------------------------------

# We edit HKCU:\Environment (user-level PATH) so the change persists
# across PowerShell sessions without admin rights. The current session
# does not pick it up automatically — that's why we print the
# "reopen PowerShell" note at the end.
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
