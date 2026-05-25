# Release pipeline

## What gets built

`.github/workflows/release.yml` invokes goreleaser (config:
[`.goreleaser.yaml`](../.goreleaser.yaml)) on every `v*.*.*` tag push.
Goreleaser produces, for each (os, arch) in the matrix:

- `fdh_<version>_<os>_<arch>.tar.gz` (or `.zip` on Windows) + `.sha256`
- `forge-installer_<version>_<os>_<arch>.tar.gz` (back-compat stub) + `.sha256`
- `.deb` and `.rpm` packages for `linux/amd64` and `linux/arm64`
- A Homebrew formula in `askenaz-dev/homebrew-tap` (when the
  internal tap is wired — placeholder until task 1.4)
- A winget manifest in `askenaz-dev/winget-pkgs` (same caveat)

The artifact-naming convention is part of the contract: `scripts/install.sh`
and `scripts/install.ps1` resolve URLs against `fdh_<version>_<os>_<arch>.<ext>`.
Renaming the pattern requires a corresponding update in both scripts and
the manifest publisher.

## Build runner choice (Taskfile, not Make)

The local `Taskfile.yml` uses the [Task](https://taskfile.dev) runner.
CI uses goreleaser directly. Reasons:

- single static binary on macOS, Linux, and Windows
- identical syntax across all three platforms
- no `make`-on-Windows friction for pilot devs
- goreleaser already encodes the matrix + packaging + tap-publishing
  steps; layering Task on top would duplicate the cross-build logic

## Versioning

Tags follow `vMAJOR.MINOR.PATCH` (semver). Pre-release suffixes (`-rc.1`,
`-beta.2`) are permitted. The workflow reads the tag and stamps it into the
binary via `-ldflags`. Manual runs via `workflow_dispatch` can override
the version string for ad-hoc builds.

## Distribution channel

Released artifacts publish to forge's internal package manager via
the `publish` job in `release.yml`. The job:

1. Downloads every artifact from the goreleaser job.
2. Uploads binaries / packages first (per file, with HTTP PUT).
3. Refreshes `${PKG_BASE_URL}/fdh/manifest.json` **last**. This is the
   atomic switch: if any binary upload fails, the manifest still points
   at the previous release and `install.sh`/`install.ps1` keep working.

Hosts the artifact shape (tar.gz + zip + .sha256 + .deb + .rpm) supports
unchanged:

- **Nexus Repository (raw)** — PUT to a raw repo.
- **JFrog Artifactory (Generic)** — same shape, same flow.
- **GitHub Packages** — release-asset upload via `gh release upload`.

The publish step is gated on `PKG_BASE_URL`/`PKG_TOKEN` being set. With
them unset (current pilot state), the goreleaser job still runs and
artifacts land in CI cache, but nothing publishes — useful for testing
the build matrix before the host is wired.

## `manifest.json` shape

```json
{
  "latest": "v0.5.2",
  "versions": {
    "v0.5.2": {
      "linux_amd64":   { "url": "fdh/v0.5.2/fdh_v0.5.2_linux_amd64.tar.gz",
                         "sha256": "abcd..." },
      "darwin_arm64":  { "url": "...", "sha256": "..." },
      "windows_amd64": { "url": "...", "sha256": "..." }
    },
    "v0.5.1": { ... }
  }
}
```

Install scripts parse the file with a regex (`install.sh`) or
`Invoke-RestMethod` (`install.ps1`). No JSON parser dependency required.

## Pilot binaries: unsigned + checksum (Q2)

Per the resolved design Q2, pilot binaries ship **unsigned**. The accompanying
`.sha256` file is the integrity check. Developers verify with:

```sh
# macOS / Linux
shasum -a 256 -c fdh-<version>-<os>-<arch>.tar.gz.sha256

# Windows (PowerShell)
$expected = (Get-Content .\fdh-<version>-windows-amd64.tar.gz.sha256).Split(' ')[0]
$actual   = (Get-FileHash .\fdh-<version>-windows-amd64.tar.gz -Algorithm SHA256).Hash.ToLower()
if ($expected -ne $actual) { throw "checksum mismatch" }
```

Code signing for macOS notarization and Windows Authenticode is in scope
for the `ops-readiness` change, not this one.

## Rollback

Tags are immutable. To pull a release, delete its artifacts from the
package manager and publish a fixed version with a higher patch number.
Do not retag.
