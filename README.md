# fdh

Cross-platform CLI that installs spec-compliant [Agent Skills](https://agentskills.io) to four AI coding agents — Claude Code, GitHub Copilot, OpenAI Codex, OpenCode — from a shared Git-backed skill registry.

This repository holds the **implementation**. The **specification** that governs every requirement here lives in the OpenSpec hub at [askenaz-dev/forge-development-hub](https://github.com/askenaz-dev/forge-development-hub):

- Hub: `forge-development-hub`
- Specs: `forge-development-hub/openspec/specs/`

If a behavior of the installer disagrees with the spec, the spec wins. Open a change in the hub to alter requirements; do not change behavior here without a corresponding spec update.

## Status

Pre-release. See [`docs/quickstart.md`](docs/quickstart.md).

## Installation

```sh
# macOS / Linux — one-liner
curl -fsSL https://raw.githubusercontent.com/askenaz-dev/forge-development-hub-cli/main/scripts/install.sh | bash
```

```powershell
# Windows — PowerShell
iwr https://raw.githubusercontent.com/askenaz-dev/forge-development-hub-cli/main/scripts/install.ps1 | iex
```

```sh
# npm (zero-install via npx, or persistent via -g)
npx @askenaz-dev/fdh init
npm i -g @askenaz-dev/fdh

# Linux packages (downloaded from GitHub Releases)
sudo apt install ./fdh_<version>_linux_amd64.deb     # Debian / Ubuntu
sudo rpm -ivh fdh_<version>_linux_amd64.rpm          # Fedora / RHEL
```

Binaries live on [GitHub Releases](https://github.com/askenaz-dev/forge-development-hub-cli/releases). To download from a private mirror instead, set `FDH_RELEASES_BASE` (and optionally `FDH_LATEST_URL`) before running the installer.

For air-gapped installs and PowerShell `ExecutionPolicy` workarounds, see [`docs/install.md`](docs/install.md). Stable exit codes are documented in [`docs/exit-codes.md`](docs/exit-codes.md).

## Layout

```
cmd/fdh/                   # main + root cobra command
internal/cli/              # one file per CLI subcommand
pkg/registry/              # Registry interface + GitRegistry implementation
pkg/adapters/              # manifest-driven agent path map
pkg/bundle/                # skill bundle parsing, validation, canonical hash
pkg/portability/           # portability lint engine
pkg/provenance/            # .skill-meta.yaml sidecar + frontmatter breadcrumb
internal/testutil/         # shared fixtures and helpers
docs/                      # quickstart, release notes, adapter reference
```

## Build

```
task build      # build the binary for the current host
task test       # unit + integration tests
task lint       # golangci-lint
task e2e        # end-to-end test against fixture registry
task release    # produce all five platform archives
```

[Task](https://taskfile.dev) is used as the build runner: a single cross-platform binary, identical syntax on macOS/Linux/Windows, no `make` dependency.

## License

MIT — see [LICENSE](LICENSE).

## Contributing

Contributions land via PR against `main`. CI must pass on macOS, Linux, and Windows before merge. Any change to runtime behavior requires a corresponding OpenSpec change in the hub repository.
