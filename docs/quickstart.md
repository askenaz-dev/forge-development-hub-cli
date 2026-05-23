# fdh quickstart

This guide walks a Falabella developer through installing the CLI, verifying
their machine, and installing their first skill from the pilot registry.

## 1. Install the binary

Download the archive for your platform from the internal package manager,
extract it, and place the binary on your `PATH`.

```sh
# macOS / Linux
curl -O https://pkg.falabella.internal/fdh/<version>/fdh-<version>-darwin-arm64.tar.gz
curl -O https://pkg.falabella.internal/fdh/<version>/fdh-<version>-darwin-arm64.tar.gz.sha256
shasum -a 256 -c fdh-<version>-darwin-arm64.tar.gz.sha256
tar -xzf fdh-<version>-darwin-arm64.tar.gz
sudo mv fdh-<version>-darwin-arm64/fdh /usr/local/bin/
```

```powershell
# Windows
Invoke-WebRequest https://pkg.falabella.internal/fdh/<version>/fdh-<version>-windows-amd64.tar.gz -OutFile installer.tar.gz
Invoke-WebRequest https://pkg.falabella.internal/fdh/<version>/fdh-<version>-windows-amd64.tar.gz.sha256 -OutFile installer.tar.gz.sha256
# Verify the checksum manually against installer.tar.gz.sha256
tar -xzf installer.tar.gz
# Move fdh.exe somewhere on PATH (e.g. C:\Tools\)
```

**Pilot note:** binaries are unsigned. The checksum is your integrity check.
Signed releases land with the `ops-readiness` change.

Confirm the install:

```sh
fdh --version
```

## 2. Point at the pilot registry

```sh
fdh config set registry.url https://git.falabella.internal/skills/registry.git
fdh config set registry.branch main
```

The registry is a regular Git repository laid out per the
[bundle-and-registry spec](../../falabella-development-hub/openspec/specs/skill-bundle-and-registry/spec.md).
You can also point at a local clone via `registry.local_path` for air-gapped use.

## 3. Run `doctor`

```sh
fdh doctor
```

Doctor reports:

- which AI agents it detected on your machine (Claude Code, Copilot, Codex, OpenCode)
- which directories it would write to for each agent
- whether the registry is reachable

Fix any `error` lines before moving on — they indicate a missing agent, an
unwritable directory, or an unreachable registry.

## 4. Search and install

```sh
fdh search owasp
fdh install security/owasp-review
```

Installation:

- resolves the latest version from the registry
- verifies the bundle's canonical SHA-256
- runs the portability lint
- writes the bundle to every directory the target agents read (up to three
  paths per scope, including `.claude/skills/`, `.agents/skills/`, and
  `.github/skills/` at project scope)
- drops a `.skill-meta.yaml` sidecar next to each `SKILL.md` so `list` can
  report what's installed and where it came from

## 5. List what you have

```sh
fdh list
fdh list --json | jq .
```

## 6. Optional: targeted install

```sh
# Only install for Claude Code, ignoring other detected agents
fdh install security/owasp-review --agent claude-code

# Install at user scope explicitly, even if the cwd is inside a git repo
fdh install code-review/standard --scope user
```

## 7. Get help

```sh
fdh --help
fdh install --help
```

If something doesn't behave like the spec, file an issue against the
OpenSpec hub repository so the requirements change before the implementation.
