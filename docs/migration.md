# Migration: `forge-installer` → `fdh`

The CLI is being renamed from `forge-installer` to `fdh` (forge
Development Hub). This page documents what changed, why, and exactly what
to do.

## Summary of changes

| What                  | Before                                   | After                          |
| --------------------- | ---------------------------------------- | ------------------------------ |
| Binary name           | `forge-installer`                    | `fdh`                          |
| Repository            | `forge/skill-installer`              | `forge/fdh`                |
| Go module             | `github.com/forge/skill-installer`   | `github.com/forge/fdh`     |
| Per-user config dir   | `~/.config/forge-installer/`         | `~/.config/fdh/`               |
| Release tarball name  | `forge-installer-<v>-<os>-<arch>.tar.gz` | `fdh-<v>-<os>-<arch>.tar.gz` |
| Slash-name in docs    | `/opsx:install` references               | `fdh install` references       |

Command surface, exit codes, JSON shapes, adapter manifest, registry
layout: **unchanged**. The rename is cosmetic + organizational.

## What you need to do

### As a developer using the CLI

1. Install the new `fdh` binary. As of the CLI-distribution change, three
   first-class channels are supported (see [`install.md`](./install.md) for
   full details):

   ```sh
   # one-liner installer (macOS / Linux)
   curl -fsSL https://raw.githubusercontent.com/askenaz-dev/forge-development-hub-cli/main/scripts/install.sh | bash

   # PowerShell (Windows)
   iwr https://${FDH_RELEASES_BASE}/fdh/install.ps1 | iex

   # Homebrew (when the internal tap is published)
   brew install askenaz-dev/tap/fdh

   # Linux package
   sudo apt install fdh    # or: sudo dnf install fdh
   ```

   Tarball downloads from the package manager still work for air-gapped or
   pinned-version installs.

2. Run `fdh config migrate` once to move your per-user config to the new
   location.
3. Update any scripts that hard-coded `forge-installer` to use `fdh`.
4. (Optional) Remove the deprecated stub binary from your `PATH` once
   nothing references it.

### Why the one-liner instead of "download a tarball"?

The tarball flow required the user to: download, verify the SHA-256 by
hand, extract, move the binary onto PATH, and edit a shell rc file. The
one-liner does all five steps in a single command, idempotently, with the
SHA-256 verified for you. It still ships the same binary — what changed
is who runs the boilerplate.

### As a CI pipeline owner

If your CI calls the installer:

```diff
- - run: forge-installer install code-review/checklist
+ - run: fdh install code-review/checklist
```

If your CI installs the binary, switch download URLs from
`forge-installer-<version>-<os>-<arch>.tar.gz` to
`fdh-<version>-<os>-<arch>.tar.gz`.

The legacy artifact name is published for 90 days after the rename ships,
acting as a stub that forwards to `fdh` if it's on PATH. Your pipelines
keep working during that window but emit a deprecation warning on every
invocation — please migrate within the 90 days.

### As an automation owner outside CI

Anywhere `forge-installer` is referenced (Slack docs, runbooks,
Confluence pages, Makefile rules):

```
forge-installer → fdh
```

Mechanical find/replace is safe — every subcommand, every flag, every
exit code is identical.

## The deprecation window

For 90 days after the rename ships:

- The legacy binary `forge-installer` continues to be published as a
  stub that prints `DEPRECATED: ...` on stderr and forwards args to `fdh`
  if it is on PATH. If `fdh` is missing, the stub exits with code 127 and
  instructs the user to install it.
- The CLI reads from the legacy config directory
  (`~/.config/forge-installer/`) when the new directory is missing the
  requested file, and emits a one-line stderr warning recommending
  `fdh config migrate`.
- Documentation links use the new binary name; the legacy name appears
  only in migration notes (this page) and the deprecation messages.

After the 90 days:

- The stub binary stops being published.
- The legacy config fallback is removed.
- Pipelines or scripts that still reference `forge-installer` will
  fail with "command not found".

The exact sunset date is documented in [`release.md`](./release.md).

## `fdh config migrate` — what it does

```
$ fdh config migrate
Migrated 2 file(s) from ~/.config/forge-installer to ~/.config/fdh:
  - config.yaml
  - adapters.yaml
```

The command is idempotent: re-running it after migration prints
`nothing to migrate (legacy config not found)`. If the new path already
contains a file, the legacy file is skipped (never clobbered) and the
command reports it under `Skipped`.

## FAQ

**Why rename now?**
The product name "forge Development Hub" is what the org wants to
build a brand around; `forge-installer` was always a working title.
Renaming once, before the broader rollout, is much cheaper than living
with a misleading name forever.

**Will my installed skills break?**
No. The on-disk format (`.skill-meta.yaml` sidecars + `installed_from:`
frontmatter breadcrumb) is unchanged. `fdh list` will read every sidecar
written by the legacy `forge-installer` correctly.

**Can I run both binaries side-by-side during the transition?**
Yes. The stub binary respects `PATH` lookup of `fdh`; if you have both on
`PATH`, every invocation of `forge-installer` forwards to `fdh` with
its arguments. There is no double-action.

**What if I push back on the rename?**
This is the moment to push back. After the 90-day stub window closes,
restoring the legacy name requires a new change proposal and a new
release. Open an issue in `forge/fdh` before the sunset date if you
have a hard blocker.
