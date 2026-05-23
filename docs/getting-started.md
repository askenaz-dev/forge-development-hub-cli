# Getting started — from zero to your first skill

This is the end-to-end walkthrough: bootstrap a registry, install the CLI,
install your first skill into your AI agents. Follow the steps in order
the first time; bookmark the section index for later reference.

## Two paths

- **Through the portal** (recommended for most developers) — browse
  skills, copy the install command, and follow the guided install flow.
  Go to <https://fdh.falabella.internal> (or `http://localhost:3000` in
  local dev). Sign in with your Falabella identity via Keycloak.
- **Direct via the CLI** (the rail underneath the portal — also covered
  in this document). Use this when scripting onboarding setups or when
  the portal is unavailable.

The portal and the CLI talk to the same registry; the only difference is
the surface developers interact with. Everything in this document is the
CLI path — the portal essentially renders these same commands as
copy-buttons.

## Sections

1. [Prerequisites](#1-prerequisites)
2. [Build (or install) the CLI](#2-build-or-install-the-cli)
3. [Bootstrap your skill registry](#3-bootstrap-your-skill-registry)
4. [Connect the CLI to the registry](#4-connect-the-cli-to-the-registry)
5. [Verify with `doctor`](#5-verify-with-doctor)
6. [Search and install your first skill](#6-search-and-install-your-first-skill)
7. [Customize the seed skills](#7-customize-the-seed-skills)
8. [Push the registry to a real Git host](#8-push-the-registry-to-a-real-git-host)
9. [Onboard a teammate](#9-onboard-a-teammate)
10. [Run the portal locally](#10-run-the-portal-locally)

---

## 1. Prerequisites

| Need                     | Why                                        |
| ------------------------ | ------------------------------------------ |
| Go 1.25+                 | Build the CLI from source until release-channel binaries land |
| Git                      | The registry IS a Git repository           |
| One AI agent installed   | Claude Code, Copilot, Codex, or OpenCode — so the install path lands somewhere your tools actually read |

Check:

```sh
go version    # >= 1.25
git --version # any recent
```

## 2. Build (or install) the CLI

### From source (today)

```sh
# clone the installer repo
git clone https://github.com/falabella/fdh.git
cd skill-installer

# build
go build -o bin/fdh ./cmd/fdh

# verify
./bin/fdh --version
```

Put `bin/fdh` (or `.exe` on Windows) somewhere on your `PATH`:

```sh
# Linux / macOS
sudo cp bin/fdh /usr/local/bin/

# Windows (PowerShell, admin)
Copy-Item bin\fdh.exe C:\Tools\
# then add C:\Tools to your PATH
```

### From the internal package manager (once released)

Once the release pipeline publishes to Nexus / JFrog / GitHub Packages, the
flow is: download the tar.gz for your platform, verify the SHA-256, extract,
place the binary on `PATH`. The full recipe is in
[quickstart.md](quickstart.md).

## 3. Bootstrap your skill registry

A registry is a Git repository with a specific layout (see
[`skill-bundle-and-registry`](../../falabella-development-hub/openspec/specs/skill-bundle-and-registry/spec.md)).
Two ways to create one:

### Option A — Seed instantly with the 8 SDLC skills (recommended for a first run)

The installer repo ships a developer utility that creates a fully-populated
registry on disk in seconds. Use this to get something working before
investing in a real host.

```sh
# From inside the skill-installer repo:
go run ./scripts/build-fixture-registry /path/to/my-registry
```

Output:

```
published requirements/user-story-generation@1.0.0  hash=362fd8913de8
published architecture/adr-generation@1.0.0         hash=27159ff4818e
published development/pr-description-writer@1.0.0   hash=7d06edbbc0cf
published code-review/checklist@1.0.0               hash=af9cb9c4d783
published testing/unit-test-generation@1.0.0        hash=3825b87940c0
published security/owasp-quick-review@1.0.0         hash=9274a5962f46
published cicd/release-notes-generation@1.0.0       hash=b266609c94ff
published operations/runbook-template@1.0.0         hash=f17cab73a14d

Registry built at /path/to/my-registry
```

That gives you 8 portable seed skills (one per SDLC phase from the
`installer-core` change appendix), spec-compliant layout, valid hashes,
ready to install.

Then make it a Git repo:

```sh
cd /path/to/my-registry
git init -b main
git add -A
git commit -m "seed: 8 SDLC skills via build-fixture-registry"
```

### Option B — Start empty and grow

If you'd rather author every skill yourself:

```sh
mkdir -p /path/to/my-registry
cd /path/to/my-registry
git init -b main

# Minimum: an empty catalog the CLI can read.
cat > index.json <<'EOF'
{
  "schema_version": 1,
  "registry": "file:///path/to/my-registry",
  "skills": []
}
EOF
git add -A && git commit -m "init: empty registry"
```

You'll need to author bundles and update `index.json` + `manifest.json`
by hand for each skill until the `installer-write-flows` change lands
a `publish` command.

## 4. Connect the CLI to the registry

```sh
# Point the CLI at your local clone
fdh config set registry.local_path /path/to/my-registry

# (Later, once you've pushed the registry to a remote)
fdh config set registry.url https://git.example/skills/registry.git
fdh config set registry.branch main
```

Confirm:

```sh
fdh config list
```

You should see your `registry.local_path` (and optionally `registry.url`)
listed. The config persists at `~/.config/fdh/config.yaml`
(or the OS equivalent on Windows).

## 5. Verify with `doctor`

```sh
fdh doctor
```

Expected output structure:

```
Installer:    v0.1.0
Home dir:     /home/you
Project root: /work/your-project  (or "none — user scope only")

Registry:
  source: git:/path/to/my-registry  [reachable]

Agents:
  claude-code  DETECTED
    user    writable-creatable     /home/you/.claude/skills
    project writable-creatable     /work/your-project/.claude/skills
  copilot      DETECTED
    user    writable-creatable     /home/you/.copilot/skills
    user    writable-creatable     /home/you/.agents/skills
    project writable-creatable     /work/your-project/.github/skills
    project writable-creatable     /work/your-project/.claude/skills
    project writable-creatable     /work/your-project/.agents/skills
  codex        not detected
    ...
  opencode     DETECTED
    ...
```

If an agent shows `not detected` and you DO have it installed, edit the
adapter map (`~/.config/fdh/adapters.yaml`) and adjust
that agent's `detect:` probes — see [adapters.md](adapters.md).

If a path shows `unwritable`, fix the permissions on its parent directory.

## 6. Search and install your first skill

```sh
fdh search owasp
```

```
NAMESPACE/NAME               VERSION  SCAN  DESCRIPTION
security/owasp-quick-review  1.0.0    pass  Run an OWASP top-10 sweep over a change set...
```

Install it:

```sh
fdh install security/owasp-quick-review
```

```
Installed security/owasp-quick-review@1.0.0
  scope:    project
  registry: git:/path/to/my-registry
  hash:     9274a5962f46...
  agents:   claude-code,codex,copilot,opencode
  wrote:
    - /work/your-project/.claude/skills/owasp-quick-review  (serves: claude-code,copilot,opencode)
    - /work/your-project/.agents/skills/owasp-quick-review  (serves: codex,copilot,opencode)
    - /work/your-project/.github/skills/owasp-quick-review  (serves: copilot)
```

Open your AI agent in this project — it should see the new skill.

List everything currently installed:

```sh
fdh list
```

### Common variants

```sh
# Install only for Claude Code
fdh install security/owasp-quick-review --agent claude-code

# Install at your home directory instead of the current project
fdh install code-review/checklist --scope user

# Install a specific version
fdh install code-review/checklist@1.0.0

# Get JSON output for scripts
fdh install security/owasp-quick-review --json
```

## 7. Customize the seed skills

The 8 seed skills are portable, Falabella-flavored starting points. You'll
almost certainly want to change them to match your team's conventions.

For each skill you want to customize:

```sh
# 1. Find the source bundle in your registry clone
cd /path/to/my-registry
ls skills/code-review/checklist/versions/1.0.0/bundle/

# 2. Edit SKILL.md (and any references/scripts/assets)
$EDITOR skills/code-review/checklist/versions/1.0.0/bundle/SKILL.md
```

After editing, the bundle hash WILL change. Today (before
`installer-write-flows` lands) the fastest way to republish is:

```sh
# Re-run the fixture builder against an updated copy of buildSeedSkills(),
# OR manually:
go run ./scripts/build-fixture-registry /path/to/my-registry-fresh
```

For now treat editing as: clone the registry repo, edit the bundle,
recompute the hash with a small Go program (or wait for the `publish`
command in the next change).

### Authoring rules to keep in mind

- Keep `portable: true` unless your skill truly needs a Claude-only
  feature (`$ARGUMENTS`, `!`cmd``, `allowed-tools`, etc.). The portability
  lint will reject portable skills that leak those — see
  [portability.md](portability.md).
- The `name` field MUST match the bundle's directory name AND be
  kebab-case (`^[a-z0-9]+(-[a-z0-9]+)*$`).
- `description` should be one sentence focused on WHEN to invoke the
  skill. Agents use it to decide whether to load the skill.

## 8. Push the registry to a real Git host

Local-only works for solo experimentation. For a team you want a remote.

```sh
cd /path/to/my-registry

# Pick a Git host: GitHub, GitLab, Gitea, Bitbucket — any host works.
git remote add origin https://git.example/skills/registry.git
git push -u origin main

# Then on every machine that uses the registry:
fdh config set registry.url https://git.example/skills/registry.git
# (drop the local_path setting if you previously set one)
```

The installer clones the remote into a per-machine cache the first time
it talks to a remote URL, and refreshes on subsequent reads via
`git fetch` + a hard reset to `origin/main`. Cached data is used
automatically if the remote becomes unreachable.

### Access control during the pilot

For the 30-dev pilot, Git push permissions ARE the access control:

- Anyone who can clone the registry repo can `fdh
  install`.
- Anyone who can push to the registry repo can publish a new skill or
  version (manually for now, via the `publish` command later).
- Use your Git host's branch protection rules to gate `main` behind
  reviewer approval — that's the pilot's approval workflow until the
  `governance` change adds OIDC + server-side RBAC.

## 9. Onboard a teammate

Once the registry is on a remote, a new teammate's full setup is:

```sh
# 1. Install the CLI (build from source today, package manager later)
git clone https://github.com/falabella/fdh.git
cd skill-installer
go build -o ~/.local/bin/fdh ./cmd/fdh

# 2. Point at the shared registry
fdh config set registry.url https://git.example/skills/registry.git

# 3. Verify
fdh doctor

# 4. Install what they need
cd ~/work/some-project
fdh install code-review/checklist
fdh install security/owasp-quick-review
```

For golden-path machine setup (a single command that installs many skills
at once), wait for the `installer-write-flows` change which adds
`fdh provision --manifest team-defaults.yaml`.

## Troubleshooting

| Symptom                                               | Fix                                                                              |
| ----------------------------------------------------- | -------------------------------------------------------------------------------- |
| `doctor` reports `not detected` for an agent you have | Edit `~/.config/fdh/adapters.yaml` and adjust the detect probes  |
| `install` exits with code 3                           | Check `registry.local_path` or `registry.url`; the configured location is unreachable |
| `install` exits with code 4                           | Portability violation in the skill — run with `--verbose` to see the rule and line number |
| `install` exits with code 5                           | No agents detected and none compatible — `doctor` will explain                   |
| `install` exits with code 6                           | Filesystem permission denied — `doctor` will name the unwritable path            |
| Skill installed but the agent doesn't see it          | Confirm the agent's read path matches `fdh list`; the agent may need a restart |

## 10. Run the portal locally

The portal is a web UI that wraps the CLI: browse the catalog, copy
install commands, sign in via Keycloak. It's the recommended discovery
surface for most developers — and you can run it locally end-to-end.

```sh
# From the repo root, one command brings up Keycloak + API + Web +
# the fixture registry:
docker compose up
```

When the API logs `registry refreshed skill_count=8`, open
<http://localhost:3000>.

Local dev seeds three users for testing each portal role (`admin`,
`author`, `consumer`). Full instructions: see [`local-dev.md`](./local-dev.md).

## What's NOT here yet

These features are designed for the next changes; not in the current installer:

- `fdh update` / `pin` / `unpin` / `remove` / `provision` / `publish`
  → coming with the `installer-write-flows` change.
- Server-side registry with web UI, RBAC, OIDC, scan gate
  → coming with `registry-mvp`, `governance`, `scan-gate`, `web-ui`.
- Code-signed binaries, Homebrew tap, apt/yum repo, Windows MSI/Winget
  → coming with `ops-readiness`.

The full roadmap and order is in the project README and the archived
[installer-core](../../falabella-development-hub/openspec/changes/archive/2026-05-22-installer-core/proposal.md)
change.
