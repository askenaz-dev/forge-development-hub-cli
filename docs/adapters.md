# Adapter map reference

The installer treats every supported AI coding agent as a row in a YAML
table. Adding or modifying an agent never requires recompiling the binary.

## Where the manifest lives

1. **Embedded default** — `pkg/adapters/builtin.yaml`, compiled into the
   binary via `go:embed`. The shipped defaults cover the four pilot agents:
   `claude-code`, `copilot`, `codex`, `opencode`.
2. **User override** — a YAML file at `~/.config/fdh/adapters.yaml`
   (or the OS-equivalent on Windows). Each agent entry in the override
   REPLACES the embedded entry with the same `id`; agents not mentioned in
   the override are preserved unchanged.

You can also pass `--config` to point at a non-default config file; the
override path is set via `fdh config set adapters.override <path>`.

## Default paths (Q3 belt-and-braces)

The installer fans each install out to the **union of every documented
path** of the target agents, deduplicated. For the full four-agent default:

| Agent       | User scope                          | Project scope                                              |
| ----------- | ----------------------------------- | ---------------------------------------------------------- |
| claude-code | `~/.claude/skills/`                 | `.claude/skills/`                                          |
| copilot     | `~/.copilot/`, `~/.agents/skills/`  | `.github/skills/`, `.claude/skills/`, `.agents/skills/`    |
| codex       | `~/.agents/skills/`                 | `.agents/skills/`                                          |
| opencode    | `~/.agents/skills/`, `~/.claude/...`| `.agents/skills/`, `.claude/skills/`                       |

Deduplicated union for "install to all four":

- **Project scope** — three paths: `.claude/skills/`, `.agents/skills/`, `.github/skills/`
- **User scope** — three paths: `~/.claude/skills/`, `~/.agents/skills/`, `~/.copilot/skills/`

## Adding a new agent

Create or extend `~/.config/fdh/adapters.yaml`:

```yaml
agents:
  - id: gemini-cli
    display_name: Gemini CLI
    source_doc_url: https://geminicli.com/docs/cli/skills/
    verified_on: "2026-05-22"
    detect:
      - type: exec-on-path
        name: gemini
      - type: dir-exists
        path: "~/.gemini"
    paths:
      user:
        - "~/.gemini/skills/<name>/"
      project:
        - ".gemini/skills/<name>/"
```

Rules:

- `id` must be kebab-case and unique across the manifest.
- `<name>` in a path template is substituted with the skill's directory name
  at install time.
- Paths beginning with `~/` expand to the user's home; relative paths are
  anchored at the detected project root.

## Probe types

| Type              | What it checks                                                                                    |
| ----------------- | ------------------------------------------------------------------------------------------------- |
| `dir-exists`      | A directory exists at the configured path. Tilde expansion is applied before stat.                |
| `exec-on-path`    | A binary by the given name is on `PATH` (uses Go's `exec.LookPath`).                              |
| `shell-exit-zero` | A one-line shell command returns exit 0. POSIX: `sh -c <cmd>`. Windows: `cmd /C <cmd>`.           |

Every probe MUST be safe to evaluate from any environment without elevated
privileges. An agent is detected if **any** of its probes succeed.

## Doctor verifies the manifest

```sh
fdh doctor
```

For every detected agent, doctor reports each declared path as:

- `writable` — directory exists and current user can write
- `writable-creatable` — directory doesn't exist yet but its parent is writable
- `unwritable` — neither the directory nor a creatable ancestor accepts writes

If you intentionally don't want the installer to write to a certain path,
edit your user override and remove that path from the agent entry.
