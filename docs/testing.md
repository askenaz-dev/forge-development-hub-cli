# Testing guide

How to verify fdh works end-to-end: from `go test` to a real
binary installing real skills into real agent directories.

## 1. Run the test suite

```sh
task test
```

Or the underlying command:

```sh
go test -count=1 ./...
```

Expected result on a clean checkout (Windows, Linux, or macOS):

```
ok  internal/cli       ~9s    (24 tests — pipeline E2E + golden JSON + binary smoke)
ok  internal/testutil  ~2s    (4  tests — static fixture skills)
ok  pkg/adapters       ~2s    (18 tests — manifest, paths, probes, writability)
ok  pkg/bundle         ~2s    (15 tests — parse, validate, hash, exec-bit)
ok  pkg/portability    ~2s    (19 tests — every lint rule)
ok  pkg/provenance     ~2s    (12 tests — sidecar + breadcrumb + CRLF)
ok  pkg/registry       ~2s    (11 tests — Git registry + refresh + hash mismatch + drift)

97+ tests, 0 failures.
```

The `internal/testutil` and `pkg/registry` suites include an integration
test that creates a real local Git remote, clones it, publishes a second
commit, and verifies refresh picks up the new commit. That test is the
authoritative validation that the registry refresh path works end-to-end.

## 2. Test specific packages

```sh
go test -v -run TestPipeline_         ./internal/cli/...        # full install pipeline
go test -v -run TestGitRegistry_      ./pkg/registry/...        # registry interface
go test -v -run TestLint_             ./pkg/portability/...     # every lint rule
go test -v -run TestInjectBreadcrumb_ ./pkg/provenance/...      # breadcrumb byte-identity
```

## 3. Build the binary

```sh
task build
# or
go build -o bin/fdh ./cmd/fdh
```

On Windows the binary is `bin/fdh.exe`.

## 4. Bootstrap a fixture registry

The repo ships with a small dev utility that creates a spec-compliant
registry on disk, so you can drive the binary end-to-end without standing
up a real Git host:

```sh
go run ./scripts/build-fixture-registry ./tmp/registry
```

Output:

```
published code-review/standard@1.0.0  hash=5d4e7766d04f
published security/owasp-review@1.2.0  hash=9852c6caf381
…
Registry built at ./tmp/registry
```

## 5. End-to-end smoke against the fixture

In a fresh, isolated environment (no leakage from the developer's real
home directory):

### Linux / macOS

```sh
export HOME="$(mktemp -d)"
mkdir -p "$HOME/.claude" "$HOME/.agents" "$HOME/.copilot"

WORK="$(mktemp -d)"
cd "$WORK"
git init -b main

./bin/fdh config set registry.local_path /absolute/path/to/tmp/registry
./bin/fdh doctor
./bin/fdh search owasp
./bin/fdh install security/owasp-review
./bin/fdh list
```

### Windows (Git Bash or PowerShell)

```powershell
$env:HOME = "$env:TEMP\fab-test-home"
$env:USERPROFILE = $env:HOME
New-Item -ItemType Directory -Force -Path "$env:HOME\.claude","$env:HOME\.agents","$env:HOME\.copilot" | Out-Null

$work = "$env:TEMP\fab-test-work"
New-Item -ItemType Directory -Force -Path $work | Out-Null
cd $work
git init -b main

.\bin\fdh.exe config set registry.local_path "$env:TEMP\registry"
.\bin\fdh.exe doctor
.\bin\fdh.exe search owasp
.\bin\fdh.exe install security/owasp-review
.\bin\fdh.exe list
```

## 6. What to verify per command

### `doctor`

- All four agents listed (`claude-code`, `copilot`, `codex`, `opencode`).
- Each agent's user-scope path appears under `user`.
- Each agent's project-scope path appears under `project` (only if a
  project root `.git/` is detected at or above cwd).
- Each path is either `writable` or `writable-creatable`. An `unwritable`
  line should print remediation detail.
- Registry line ends with `reachable` (after `config set`).

### `search <query>`

- Returns one row per matching skill, sorted by registry order.
- Empty query returns all skills.
- Query "nonexistent-term" returns "No matches." with exit 0.

### `install <namespace>/<name>`

- Prints `Installed <ns>/<name>@<version>`.
- Prints `wrote:` followed by **3 paths at project scope** (`.claude/skills/`,
  `.agents/skills/`, `.github/skills/`) or **3 paths at user scope**
  (`~/.claude/skills/`, `~/.agents/skills/`, `~/.copilot/skills/`).
- Each path entry shows the agents it serves (e.g. `serves: claude-code,copilot,opencode`).
- After the command:
  - `find <work>/.{claude,agents,github}/skills/<skill>/SKILL.md` produces
    three files with identical bodies + one `installed_from:` line each.
  - Three `.skill-meta.yaml` sidecars exist, all with the same
    `content_hash` value.

### `install --agent claude-code --scope user`

- Writes to exactly **one** path: `$HOME/.claude/skills/<skill>/`.
- The sidecar's `target_agents` is `[claude-code]`.

### `install --json`

- Output is valid JSON matching the shape in `docs/json-output.md`.
- The `writes` array length matches the table output's path count.
- `content_hash` is a 64-character lowercase hex string.

### `list`

- One row per `(skill, scope, path)` tuple.
- After an install to all four agents at project scope, `list` shows three
  rows for that skill, each with a different path and a different agent set.

### `list --json`

- Output is a JSON array. Round-trips cleanly through `jq`.

### `config set/get/list`

- `config set bogus.key value` exits non-zero with a `unknown config key` error
  citing the supported set.
- `config get registry.url` after `config set registry.url X` prints exactly `X`.

## 7. Idempotency check

Run install twice in a row:

```sh
./bin/fdh install security/owasp-review
./bin/fdh install security/owasp-review
```

After the second run:

- No errors; the second install writes the same files.
- `grep installed_from <work>/.claude/skills/owasp-review/SKILL.md | wc -l`
  prints `1` (not `2`).

## 8. Portability lint integration

Create a deliberately broken portable skill in your fixture registry:

```yaml
---
name: broken
description: Uses Claude-only syntax in a portable skill
---
Run $ARGUMENTS now.
```

Run `install demo/broken`. Expected:

- Exit code **4** (portability violation).
- stderr lists `PORT200` with the SKILL.md line number.
- Nothing written to any agent directory.

## 9. Exit-code matrix

Verify each documented exit code is reachable:

| Code | Trigger                                                              |
| ---- | -------------------------------------------------------------------- |
| 0    | `fdh --version`                                      |
| 2    | `fdh config set bogus.key value`                     |
| 3    | `fdh install foo/bar` after `config set registry.local_path /nope` |
| 4    | install a portable skill with `$ARGUMENTS` in the body (see §8)      |
| 5    | install in an environment with no detectable agents (delete `~/.claude` etc.) |
| 6    | install while the destination directory is owned by root (Linux only) |

## 10. Cross-platform matrix

The CI workflow at `.github/workflows/ci.yml` runs the test suite on:

- `ubuntu-latest` (Linux amd64)
- `macos-latest`  (macOS arm64)
- `windows-latest` (Windows amd64)

For local cross-platform validation before pushing:

```sh
# Linux from a Linux host
GOOS=linux GOARCH=amd64 go build -o bin/installer-linux ./cmd/fdh

# Cross-compile for Windows from Linux/macOS
GOOS=windows GOARCH=amd64 go build -o bin/installer.exe ./cmd/fdh
```

The static-binary promise (no CGO) makes cross-compilation work without
toolchain installations.

## 11. Pilot dry-run checklist

Before opening the pilot to 30 developers, validate on three fresh
machines (one per OS):

- [ ] `fdh doctor` detects exactly the agents installed on that host.
- [ ] Every declared agent path resolves and is reported writable or writable-creatable.
- [ ] `fdh search` returns hits against the real (not fixture) registry.
- [ ] `fdh install` writes the bundle and sidecar to every documented path.
- [ ] The Claude Code app, opened in that project, picks up the installed skill and shows it under the appropriate scope.
- [ ] Equivalent verification in Copilot, Codex, and OpenCode where the dev uses each.
- [ ] `fdh install` then `fdh list` show the same `content_hash`.
- [ ] Removing a path and re-installing restores the file (idempotency).
- [ ] An installed skill with a known invalid frontmatter (e.g. `$ARGUMENTS` in the body of a portable skill) fails the lint with exit 4.

When all three machines pass: archive the OpenSpec change with
`/opsx:archive installer-core` and proceed to the next change.
