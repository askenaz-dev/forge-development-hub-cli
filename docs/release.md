# Release pipeline

## What gets built

`.github/workflows/release.yml` produces five release artifacts per tag:

- `fdh-<version>-darwin-arm64.tar.gz` + `.sha256`
- `fdh-<version>-darwin-amd64.tar.gz` + `.sha256`
- `fdh-<version>-linux-arm64.tar.gz` + `.sha256`
- `fdh-<version>-linux-amd64.tar.gz` + `.sha256`
- `fdh-<version>-windows-amd64.tar.gz` + `.sha256`

Each archive contains the binary, `LICENSE`, and `README.md`. Binaries are
built with `-trimpath` and `CGO_ENABLED=0` so they are statically linked
and reproducible across the matrix.

## Build runner choice (Taskfile, not Make)

The local `Taskfile.yml` and the CI workflow both use the [Task](https://taskfile.dev)
runner. Reasons documented in `README.md`:

- single static binary on macOS, Linux, and Windows
- identical syntax across all three platforms
- no `make`-on-Windows friction for pilot devs

## Versioning

Tags follow `vMAJOR.MINOR.PATCH` (semver). Pre-release suffixes (`-rc.1`,
`-beta.2`) are permitted. The workflow reads the tag and stamps it into the
binary via `-ldflags`. Manual runs via `workflow_dispatch` can override
the version string for ad-hoc builds.

## Distribution channel

Released artifacts publish to Falabella's internal package manager.
The build pipeline produces standard tar.gz + SHA-256 artifacts that any of
the three candidate hosts can serve unchanged:

- **Nexus Repository (raw)** — `.tar.gz` and `.sha256` upload to a raw repo
  via PUT, no per-format tooling needed.
- **JFrog Artifactory (Generic)** — same shape, same upload flow.
- **GitHub Packages** — release-asset upload via `gh release upload`.

The `publish-package-manager` job in `release.yml` is currently a
placeholder. Once ops confirms the host, plug in the upload step:

```yaml
- name: Publish to Nexus
  env:
    PKG_BASE_URL: ${{ vars.PKG_BASE_URL }}
    PKG_TOKEN: ${{ secrets.PKG_TOKEN }}
  run: |
    for f in dist/*.tar.gz dist/*.sha256; do
      curl -fsSL --user "$PKG_TOKEN" --upload-file "$f" \
        "${PKG_BASE_URL}/fdh/$(basename "$f")"
    done
```

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
