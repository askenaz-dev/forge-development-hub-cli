# Installing `fdh`

`fdh` is the forge Development Hub CLI. This page is the canonical
reference for installing it on macOS, Linux, and Windows.

> **Heads-up:** the download host is configured via the `FDH_PKG_HOST`
> environment variable. The placeholder default is `pkg.forge.internal`
> — replace it with the real host the platform team confirms (see
> `openspec/changes/implement-cli-distribution-and-interactive-init/tasks.md`
> task 1.3).

## macOS / Linux — one-liner

```sh
curl -fsSL https://${FDH_PKG_HOST}/fdh/install.sh | bash
```

What this does, in order:

1. Detects your OS and architecture (`darwin/arm64`, `linux/amd64`, …).
2. Fetches `https://${FDH_PKG_HOST}/fdh/manifest.json` to resolve `latest`.
3. Downloads `fdh_<version>_<os>_<arch>.tar.gz` and its `.sha256` sibling.
4. Verifies the SHA-256; aborts with exit `5` if it doesn't match.
5. Extracts `fdh` to `$HOME/.fdh/bin/fdh` and makes it executable.
6. Adds `$HOME/.fdh/bin` to your `PATH` via `~/.zshrc` or `~/.bashrc`.
7. Re-running with the same target version is a no-op (`.last-tarball-sha256`
   marker is checked first).

### Pin to a specific version

```sh
curl -fsSL https://${FDH_PKG_HOST}/fdh/install.sh | bash -s -- --version v0.5.2
```

### Override the install directory

```sh
FDH_INSTALL_DIR=/opt/fdh/bin curl -fsSL https://${FDH_PKG_HOST}/fdh/install.sh | bash
```

### Override the package host

```sh
FDH_PKG_HOST=internal-mirror.forge.cl bash install.sh
```

`FDH_PKG_HOST` is a contract of the install scripts only. The `fdh`
binary, once installed, never reads it — its single network dependency
is the Git remote configured by `registry.url`.

### Unknown shell (fish, nushell, …)

The installer detects fish and nushell and prints the matching PATH-edit
snippet so you can paste it into your shell config. For other shells, add
`$HOME/.fdh/bin` to PATH yourself.

## Windows — PowerShell

Run from a PowerShell session that already permits remote-script execution:

```powershell
iwr https://${env:FDH_PKG_HOST}/fdh/install.ps1 | iex
```

If your `ExecutionPolicy` is restrictive, save the script first and run
it explicitly:

```powershell
Invoke-WebRequest "https://$env:FDH_PKG_HOST/fdh/install.ps1" -OutFile install.ps1
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

The script:

1. Detects arch (currently `amd64` only; arm64 will be added with a
   future release).
2. Resolves the version from `manifest.json`.
3. Downloads `fdh_<version>_windows_amd64.zip` + `.sha256`.
4. Verifies the SHA-256.
5. Extracts `fdh.exe` to `$env:USERPROFILE\.fdh\bin\`.
6. Adds `$env:USERPROFILE\.fdh\bin` to your user PATH via
   `HKCU:\Environment`. Reopen PowerShell for the new PATH to take effect
   in your current shell.

### Pin to a specific version

```powershell
iwr https://${env:FDH_PKG_HOST}/fdh/install.ps1 -OutFile install.ps1
powershell -File .\install.ps1 -Version v0.5.2
```

## Homebrew (macOS / Linux, when available)

```sh
brew tap forge-internal/tools
brew install fdh
```

The tap and formula are produced by goreleaser on every tag push. The
formula tracks the same artifacts the one-liner uses.

## Linux packages (`.deb` / `.rpm`)

```sh
# Debian / Ubuntu
curl -fsSL -O https://${FDH_PKG_HOST}/fdh/<version>/fdh_<version>_linux_amd64.deb
sudo apt install ./fdh_<version>_linux_amd64.deb

# Fedora / RHEL
curl -fsSL -O https://${FDH_PKG_HOST}/fdh/<version>/fdh_<version>_linux_amd64.rpm
sudo rpm -ivh fdh_<version>_linux_amd64.rpm
```

Both packages install the binary to `/usr/lib/fdh/fdh` and create a
symlink at `/usr/local/bin/fdh` so it lands on PATH for every shell.

## winget (Windows, when available)

```powershell
winget install forge.FDH
```

Same artifact contract as the one-liner; the winget manifest is produced
by goreleaser.

## Air-gapped / pinned: tarball download

For environments that can't run the install scripts:

```sh
# macOS / Linux
curl -O https://${FDH_PKG_HOST}/fdh/<version>/fdh_<version>_darwin_arm64.tar.gz
curl -O https://${FDH_PKG_HOST}/fdh/<version>/fdh_<version>_darwin_arm64.tar.gz.sha256
shasum -a 256 -c fdh_<version>_darwin_arm64.tar.gz.sha256
tar -xzf fdh_<version>_darwin_arm64.tar.gz
sudo mv fdh /usr/local/bin/
fdh --version
```

```powershell
# Windows
Invoke-WebRequest "https://$env:FDH_PKG_HOST/fdh/<version>/fdh_<version>_windows_amd64.zip" -OutFile fdh.zip
Invoke-WebRequest "https://$env:FDH_PKG_HOST/fdh/<version>/fdh_<version>_windows_amd64.zip.sha256" -OutFile fdh.zip.sha256
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

`fdh doctor` reports detected agents, registry status, and per-path
writability. Run it after any install to confirm everything's wired up.

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
| `error: checksum mismatch` | Corrupted download or wrong host. | Retry. If it persists, file an issue — manifest may be out of sync. |
| `fdh: command not found` after install | Shell rc wasn't reloaded. | `source ~/.zshrc` (or open a new shell). |
| Install script says "FDH_PKG_HOST not set; using placeholder" | The default host is unreachable until the platform team confirms it (task 1.3). | Set `FDH_PKG_HOST=<real-host>` before running. |
| Wizard fallback "wizard requires a TTY" | `fdh init` was piped or run in CI without flags. | Re-run with `--agents` and `--skills`, or `--non-interactive`. |

## Exit codes

See [`exit-codes.md`](./exit-codes.md) for the stable list. Install
scripts use `0` (success), `1` (generic), `2` (invalid usage), `3`
(unsupported OS/arch), `4` (network), `5` (checksum mismatch).
