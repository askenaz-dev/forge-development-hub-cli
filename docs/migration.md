# Migration: `falabella-installer` → `fdh`

The CLI is being renamed from `falabella-installer` to `fdh` (Falabella
Development Hub). This page documents what changed, why, and exactly what
to do.

## Summary of changes

| What                  | Before                                   | After                          |
| --------------------- | ---------------------------------------- | ------------------------------ |
| Binary name           | `falabella-installer`                    | `fdh`                          |
| Repository            | `falabella/skill-installer`              | `falabella/fdh`                |
| Go module             | `github.com/falabella/skill-installer`   | `github.com/falabella/fdh`     |
| Per-user config dir   | `~/.config/falabella-installer/`         | `~/.config/fdh/`               |
| Release tarball name  | `falabella-installer-<v>-<os>-<arch>.tar.gz` | `fdh-<v>-<os>-<arch>.tar.gz` |
| Slash-name in docs    | `/opsx:install` references               | `fdh install` references       |

Command surface, exit codes, JSON shapes, adapter manifest, registry
layout: **unchanged**. The rename is cosmetic + organizational.

## What you need to do

### As a developer using the CLI

1. Install the new `fdh` binary from the internal package manager (see
   [`getting-started.md`](./getting-started.md)).
2. Run `fdh config migrate` once to move your per-user config to the new
   location.
3. Update any scripts that hard-coded `falabella-installer` to use `fdh`.
4. (Optional) Remove the deprecated stub binary from your `PATH` once
   nothing references it.

### As a CI pipeline owner

If your CI calls the installer:

```diff
- - run: falabella-installer install code-review/checklist
+ - run: fdh install code-review/checklist
```

If your CI installs the binary, switch download URLs from
`falabella-installer-<version>-<os>-<arch>.tar.gz` to
`fdh-<version>-<os>-<arch>.tar.gz`.

The legacy artifact name is published for 90 days after the rename ships,
acting as a stub that forwards to `fdh` if it's on PATH. Your pipelines
keep working during that window but emit a deprecation warning on every
invocation — please migrate within the 90 days.

### As an automation owner outside CI

Anywhere `falabella-installer` is referenced (Slack docs, runbooks,
Confluence pages, Makefile rules):

```
falabella-installer → fdh
```

Mechanical find/replace is safe — every subcommand, every flag, every
exit code is identical.

## The deprecation window

For 90 days after the rename ships:

- The legacy binary `falabella-installer` continues to be published as a
  stub that prints `DEPRECATED: ...` on stderr and forwards args to `fdh`
  if it is on PATH. If `fdh` is missing, the stub exits with code 127 and
  instructs the user to install it.
- The CLI reads from the legacy config directory
  (`~/.config/falabella-installer/`) when the new directory is missing the
  requested file, and emits a one-line stderr warning recommending
  `fdh config migrate`.
- Documentation links use the new binary name; the legacy name appears
  only in migration notes (this page) and the deprecation messages.

After the 90 days:

- The stub binary stops being published.
- The legacy config fallback is removed.
- Pipelines or scripts that still reference `falabella-installer` will
  fail with "command not found".

The exact sunset date is documented in [`release.md`](./release.md).

## `fdh config migrate` — what it does

```
$ fdh config migrate
Migrated 2 file(s) from ~/.config/falabella-installer to ~/.config/fdh:
  - config.yaml
  - adapters.yaml
```

The command is idempotent: re-running it after migration prints
`nothing to migrate (legacy config not found)`. If the new path already
contains a file, the legacy file is skipped (never clobbered) and the
command reports it under `Skipped`.

## FAQ

**Why rename now?**
The product name "Falabella Development Hub" is what the org wants to
build a brand around; `falabella-installer` was always a working title.
Renaming once, before the broader rollout, is much cheaper than living
with a misleading name forever.

**Will my installed skills break?**
No. The on-disk format (`.skill-meta.yaml` sidecars + `installed_from:`
frontmatter breadcrumb) is unchanged. `fdh list` will read every sidecar
written by the legacy `falabella-installer` correctly.

**Can I run both binaries side-by-side during the transition?**
Yes. The stub binary respects `PATH` lookup of `fdh`; if you have both on
`PATH`, every invocation of `falabella-installer` forwards to `fdh` with
its arguments. There is no double-action.

**What if I push back on the rename?**
This is the moment to push back. After the 90-day stub window closes,
restoring the legacy name requires a new change proposal and a new
release. Open an issue in `falabella/fdh` before the sunset date if you
have a hard blocker.
