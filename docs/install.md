# Installing `fdh`

`fdh` is the Forge Development Hub CLI. This page is the canonical reference for installing it on macOS, Linux, and Windows.

Binaries are published as assets on [GitHub Releases](https://github.com/askenaz-dev/forge-development-hub-cli/releases). The install scripts and the npm wrapper all resolve and download from there by default.

## macOS / Linux — one-liner

```sh
curl -fsSL https://raw.githubusercontent.com/askenaz-dev/forge-development-hub-cli/main/scripts/install.sh | bash
```

What this does, in order:

1. Detects your OS and architecture (`darwin/arm64`, `linux/amd64`, …).
2. Calls the GitHub Releases API (`/repos/askenaz-dev/forge-development-hub-cli/releases/latest`) to resolve the latest `tag_name`.
3. Downloads `fdh_<tag>_<os>_<arch>.tar.gz` and its `.sha256` sibling from the release assets.
4. Verifies the SHA-256; aborts with exit `5` if it doesn't match.
5. Extracts `fdh` to `$HOME/.fdh/bin/fdh` and makes it executable.
6. Adds `$HOME/.fdh/bin` to your `PATH` via `~/.zshrc` or `~/.bashrc`.
7. Re-running with the same target version is a no-op (`.last-tarball-sha256` marker is checked first).

### Pin to a specific version

```sh
curl -fsSL https://raw.githubusercontent.com/askenaz-dev/forge-development-hub-cli/main/scripts/install.sh | bash -s -- --version v0.5.2
```

### Override the install directory

```sh
FDH_INSTALL_DIR=/opt/fdh/bin bash install.sh
```

### Override the releases host (private mirror)

```sh
FDH_RELEASES_BASE=https://mirror.askenaz.dev/fdh \
  FDH_LATEST_URL=https://mirror.askenaz.dev/fdh/latest.json \
  bash install.sh
```

The two env vars are independent: `FDH_RELEASES_BASE` controls where the asset tarballs live, `FDH_LATEST_URL` controls the endpoint returning `{ "tag_name": "<v...>" }`. The latter must return JSON shaped like GitHub's Releases API.

Once installed, the `fdh` binary itself ignores both vars — its network dependency is whichever registry you've configured: either the Git remote in `registry.url` (`fdh` will `git clone` lazily) or an HTTP base URL (no git on the host required — see [quickstart 2c](./quickstart.md#2c-use-an-http-registry-no-git-required)).

### Unknown shell (fish, nushell, …)

The installer detects fish and nushell and prints the matching PATH-edit snippet so you can paste it into your shell config. For other shells, add `$HOME/.fdh/bin` to PATH yourself.

## Windows — PowerShell

Run from a PowerShell session that already permits remote-script execution:

```powershell
iwr https://raw.githubusercontent.com/askenaz-dev/forge-development-hub-cli/main/scripts/install.ps1 | iex
```

If your `ExecutionPolicy` is restrictive, save the script first and run it explicitly:

```powershell
Invoke-WebRequest "https://raw.githubusercontent.com/askenaz-dev/forge-development-hub-cli/main/scripts/install.ps1" -OutFile install.ps1
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

The script:

1. Detects arch (currently `amd64` only; arm64 will be added with a future release).
2. Resolves the latest tag via the GitHub Releases API.
3. Downloads `fdh_<tag>_windows_amd64.zip` + `.sha256` from the release assets.
4. Verifies the SHA-256.
5. Extracts `fdh.exe` to `$env:USERPROFILE\.fdh\bin\`.
6. Adds `$env:USERPROFILE\.fdh\bin` to your user PATH via `HKCU:\Environment`. Reopen PowerShell for the new PATH to take effect in your current shell.

### Pin to a specific version

```powershell
iwr https://raw.githubusercontent.com/askenaz-dev/forge-development-hub-cli/main/scripts/install.ps1 -OutFile install.ps1
powershell -File .\install.ps1 -Version v0.5.2
```

## Homebrew (macOS / Linux, when available)

```sh
brew tap askenaz-dev/tap
brew install fdh
```

The tap is not yet published — pending bandwidth to maintain. Use the one-liner or the npm channel in the meantime.

## Linux packages (`.deb` / `.rpm`)

```sh
# Debian / Ubuntu (download the asset from the latest release)
curl -fsSL -O https://github.com/askenaz-dev/forge-development-hub-cli/releases/download/<tag>/fdh_<tag>_linux_amd64.deb
sudo apt install ./fdh_<tag>_linux_amd64.deb

# Fedora / RHEL
curl -fsSL -O https://github.com/askenaz-dev/forge-development-hub-cli/releases/download/<tag>/fdh_<tag>_linux_amd64.rpm
sudo rpm -ivh fdh_<tag>_linux_amd64.rpm
```

Both packages install the binary to `/usr/lib/fdh/fdh` and create a symlink at `/usr/local/bin/fdh` so it lands on PATH for every shell.

## winget (Windows, when available)

```powershell
winget install askenaz.FDH
```

Same artifact contract as the one-liner; the winget manifest will be produced by goreleaser when the source is wired.

## Air-gapped / pinned: tarball download

For environments that can't run the install scripts:

```sh
# macOS / Linux
curl -O https://github.com/askenaz-dev/forge-development-hub-cli/releases/download/<tag>/fdh_<tag>_darwin_arm64.tar.gz
curl -O https://github.com/askenaz-dev/forge-development-hub-cli/releases/download/<tag>/fdh_<tag>_darwin_arm64.tar.gz.sha256
shasum -a 256 -c fdh_<tag>_darwin_arm64.tar.gz.sha256
tar -xzf fdh_<tag>_darwin_arm64.tar.gz
sudo mv fdh /usr/local/bin/
fdh --version
```

```powershell
# Windows
Invoke-WebRequest "https://github.com/askenaz-dev/forge-development-hub-cli/releases/download/<tag>/fdh_<tag>_windows_amd64.zip" -OutFile fdh.zip
Invoke-WebRequest "https://github.com/askenaz-dev/forge-development-hub-cli/releases/download/<tag>/fdh_<tag>_windows_amd64.zip.sha256" -OutFile fdh.zip.sha256
$expected = (Get-Content .\fdh.zip.sha256).Trim().Split(" ")[0]
$actual   = (Get-FileHash .\fdh.zip -Algorithm SHA256).Hash.ToLower()
if ($expected -ne $actual) { throw "checksum mismatch" }
Expand-Archive fdh.zip -DestinationPath C:\Tools\fdh
# Add C:\Tools\fdh to your PATH manually.
```

## Verifying the install

```sh
fdh --version
fdh doctor
```

`fdh doctor` reports detected agents, registry status, and per-path writability. Run it after any install to confirm everything's wired up.

## Uninstall

```sh
# macOS / Linux
rm -rf "$HOME/.fdh"
# remove the PATH line added to ~/.zshrc or ~/.bashrc
```

```powershell
# Windows
Remove-Item -Recurse -Force "$env:USERPROFILE\.fdh"
# remove the PATH entry from HKCU:\Environment via System Properties → Environment Variables
```

## Troubleshooting

| Symptom | Diagnosis | Fix |
|---|---|---|
| `error: checksum mismatch` | Corrupted download or asset mismatch. | Retry. If it persists, file an issue. |
| `fdh: command not found` after install | Shell rc wasn't reloaded. | `source ~/.zshrc` (or open a new shell). |
| `error: could not fetch latest release info` | Outbound network blocked. | Set `FDH_RELEASES_BASE` + `FDH_LATEST_URL` to an internal mirror, or pin with `--version`. |
| Wizard fallback "wizard requires a TTY" | `fdh init` was piped or run in CI without flags. | Re-run with `--agents` and `--skills`, or `--non-interactive`. |

## Exit codes

See [`exit-codes.md`](./exit-codes.md) for the stable list. Install scripts use `0` (success), `1` (generic), `2` (invalid usage), `3` (unsupported OS/arch), `4` (network), `5` (checksum mismatch).
