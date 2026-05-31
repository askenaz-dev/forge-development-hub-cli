# fdh quickstart

This guide walks a developer through installing the CLI, verifying their machine, and installing their first skill from the askenaz-dev hub.

## 1. Install `fdh`

Pick the channel that fits your machine. Most devs use **npm**.

### Recommended — npm (works on macOS, Linux, Windows)

```sh
# Zero-install (one-off run, no PATH editing):
npx @askenaz-dev/fdh init

# Persistent install:
npm i -g @askenaz-dev/fdh
```

The npm package contains a tiny TypeScript wrapper that downloads the right Go binary for your platform (`darwin-arm64`, `darwin-amd64`, `linux-arm64`, `linux-amd64`, `windows-amd64`) on first install. Behind a corporate proxy? The postinstall honors `npm_config_https_proxy`, `HTTPS_PROXY`, and `NO_PROXY` — see [`troubleshooting.md`](./troubleshooting.md) for cert-inspection setups.

> **Why npm?** Most devs already have Node installed (Claude Code, VS Code, frontend toolchain all depend on it). The npm channel sidesteps Authenticode/Gatekeeper warnings, ships a single artifact, and gives you `fdh upgrade` for free via `npm update -g`.

### Fallback — POSIX / PowerShell one-liner

For environments without Node (headless servers, minimal containers, air-gapped VMs):

```sh
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/askenaz-dev/forge-development-hub-cli/main/scripts/install.sh | bash
```

```powershell
# Windows
iwr https://raw.githubusercontent.com/askenaz-dev/forge-development-hub-cli/main/scripts/install.ps1 | iex
```

Full details and overrides are in [`install.md`](./install.md).

### Fallback — Linux native packages

```sh
# Download the .deb / .rpm asset from the latest GitHub Release, then:
sudo apt install ./fdh_<version>_linux_amd64.deb     # Debian / Ubuntu
sudo rpm -ivh fdh_<version>_linux_amd64.rpm          # Fedora / RHEL
```

### Coming later — Homebrew tap + winget

```sh
# When a tap is published:
brew install askenaz-dev/tap/fdh

# When a winget source is published:
winget install askenaz.FDH
```

These channels are optional and unblocked by the npm channel being primary.

> Override the binary host with `FDH_RELEASES_BASE` and `FDH_LATEST_URL` if you want to install from a private mirror.

**Pilot note:** binaries are unsigned. The installers verify the SHA-256 for you. The npm channel sidesteps SmartScreen / Gatekeeper because the binary runs from `node_modules/`.

Confirm the install:

```sh
fdh --version
```

## 2. Run `fdh init` (interactive)

```sh
fdh init
```

`fdh init` is the one-stop setup:

1. Writes `~/.config/fdh/config.yaml` with a sensible registry URL.
2. Opens a wizard:
   - Step 1: pick which agents to target (Claude Code, Codex, …).
   - Step 2: pick which skills/rules/agents/hooks to install (profile defaults pre-selected).
   - Step 3: confirm.
3. Installs the selected components to the per-agent conventional paths
   (`.claude/skills/<name>/`, `.github/prompts/<name>.prompt.md`, …) and writes `.fdh/manifest.yaml` + `.fdh/lock.yaml` for reproducibility.
4. Runs `fdh doctor` to verify reachability.

For CI (or any non-TTY context):

```sh
fdh init \
  --registry-url https://github.com/askenaz-dev/forge-development-hub.git \
  --agents claude-code,codex \
  --skills design-system \
  --non-interactive
```

## 2b. Or point at the hub manually

If you prefer not to use the wizard:

```sh
fdh config set registry.url https://github.com/askenaz-dev/forge-development-hub.git
fdh config set registry.branch main
```

The registry is a regular Git repository laid out per the [bundle-and-registry spec](../../forge-development-hub/openspec/specs/skill-bundle-and-registry/spec.md). You can also point at a local clone via `registry.local_path` for air-gapped use.

## 2c. Use an HTTP registry (no git required)

`fdh` also speaks a static HTTP wire protocol — any web server hosting the registry tree under a `/v1/` prefix (`index.json`, `skills/<ns>/<name>/manifest.json`, `versions/<v>/bundle.tar.gz` + `bundle.sha256`) works. The HTTP transport skips `git clone`, doesn't require git on the host at all, and is the fastest path on a fresh machine (~kB per skill vs. ~150 MB for a full clone).

```sh
fdh config set registry.url https://fdh.askenaz.dev/v1/
# kind=auto (default) picks HTTP automatically because the URL has no .git suffix
```

The transport is chosen by `registry.kind`:

| `registry.kind` | Behavior |
|---|---|
| `auto` (default) | URL `.git`/`git@`/`ssh://` → git; `https://` without `.git` → http |
| `git` | Force git transport |
| `http` | Force HTTP transport |

Authentication options for private registries:

```sh
fdh config set registry.http.auth.bearer "$REGISTRY_TOKEN"     # bearer
fdh config set registry.http.auth.basic.user alice              # basic
fdh config set registry.http.auth.basic.pass s3cret
fdh config set registry.http.auth.client_cert /etc/ssl/cli.crt  # mTLS
fdh config set registry.http.auth.client_key  /etc/ssl/cli.key
```

Every key has an env-var override (`FDH_REGISTRY_KIND`, `FDH_REGISTRY_HTTP_BEARER`, `FDH_REGISTRY_HTTP_BASIC_USER`, `FDH_REGISTRY_HTTP_BASIC_PASS`, `FDH_REGISTRY_HTTP_API_VERSION`, `FDH_REGISTRY_HTTP_CLIENT_CERT`, `FDH_REGISTRY_HTTP_CLIENT_KEY`) — handy for CI.

Confirm with `fdh doctor`:

```
Registry:
  transport: http v1
  source: http:https://fdh.askenaz.dev/v1/?api=v1  [reachable]
```

## 3. Run `doctor`

```sh
fdh doctor
```

Doctor reports:

- which AI agents it detected on your machine (Claude Code, Copilot, Codex, OpenCode)
- which directories it would write to for each agent
- whether the registry is reachable
- whether your `.fdh/lock.yaml` matches what's on disk (drift detection)

Fix any `error` lines before moving on — they indicate a missing agent, an unwritable directory, an unreachable registry, or a managed-path drift.

## 4. Search and install

```sh
fdh search owasp
fdh install security/owasp-review
```

Installation:

- resolves the latest version from the registry (or the version pinned in `.fdh/lock.yaml`)
- verifies the bundle's canonical SHA-256
- runs the portability lint + `fdh scan` for secrets / hook injection / MCP risk
- writes the bundle to every directory the target agents read (`.claude/skills/`, `.github/prompts/`, etc.)
- drops a `.fdh-managed.yaml` sidecar next to each materialized component so `list-installed` and `doctor` know who owns what

## 5. List what you have

```sh
fdh list                                  # this project (from lock.yaml)
fdh list-installed                        # user-scope inventory from ~/.fdh/state.json
fdh list-installed --all --json | jq .    # everything, machine-readable
```

## 6. Optional: targeted install

```sh
# Only install for Claude Code, ignoring other detected agents
fdh install security/owasp-review --agent claude-code

# Install at user scope explicitly, even if cwd is inside a git repo
fdh install code-review/standard --scope user

# Install everything from the hub's `minimal` profile in one shot
fdh init --profile minimal
```

## 7. Update / repair / uninstall

```sh
fdh update                       # sync installed components against the hub
fdh update --dry-run             # preview what would change
fdh doctor                       # detect drift
fdh repair                       # re-install anything in lock but missing on disk
fdh uninstall design-system --dry-run    # preview removal
```

## 8. Get help

```sh
fdh --help
fdh install --help
```

If something doesn't behave like the spec, file an issue against the OpenSpec hub repository so the requirements change before the implementation.

## Troubleshooting

See [`troubleshooting.md`](./troubleshooting.md) for: proxies, cert inspection, package manager edge cases, cache miss, missing binary, unsupported targets.
