# JSON output reference

Every CLI command accepts `--json` to emit structured output for scripts and
onboarding pipelines. The JSON shapes documented here are part of the CLI's
public contract — pinned by the `internal/cli/golden_test.go` snapshot
tests. A breaking change to any of these shapes is a major-version bump.

## `install --json`

```json
{
  "skill": "security/owasp-review",
  "namespace": "security",
  "name": "owasp-review",
  "version": "1.2.0",
  "content_hash": "abc...64hex",
  "scope": "project",
  "registry": "git:https://reg.internal/... (clone at ...)",
  "target_agents": ["claude-code", "copilot", "codex", "opencode"],
  "writes": [
    {
      "path": "/work/proj/.claude/skills/owasp-review",
      "agents": ["claude-code", "copilot", "opencode"]
    },
    {
      "path": "/work/proj/.agents/skills/owasp-review",
      "agents": ["codex", "copilot", "opencode"]
    },
    {
      "path": "/work/proj/.github/skills/owasp-review",
      "agents": ["copilot"]
    }
  ]
}
```

## `list --json`

An array of skill records:

```json
[
  {
    "skill": "code-review/standard",
    "namespace": "code-review",
    "name": "standard",
    "version": "1.0.0",
    "source": "git:https://reg.internal/...",
    "scope": "project",
    "path": "/work/proj/.claude/skills/standard",
    "target_agents": ["claude-code", "copilot", "opencode"],
    "content_hash": "abc..."
  }
]
```

Skills installed without a valid `.skill-meta.yaml` sidecar appear with
`source: "unknown"` and `version: "unknown"` rather than failing the command.

## `doctor --json`

```json
{
  "installer_version": "v0.1.0",
  "home_dir": "/home/alice",
  "project_root": "/work/proj",
  "registry": {
    "configured": true,
    "source": "git:https://reg.internal/...",
    "reachable": true,
    "detail": ""
  },
  "agents": [
    {
      "id": "claude-code",
      "detected": true,
      "user_paths": [
        { "path": "/home/alice/.claude/skills", "state": "writable", "detail": "" }
      ],
      "project_paths": [
        { "path": "/work/proj/.claude/skills", "state": "writable-creatable", "detail": "will be created under /work/proj" }
      ]
    }
  ],
  "issues": []
}
```

`state` is one of `writable`, `writable-creatable`, `unwritable`. The
command exits non-zero when `issues` contains any entry with `severity: error`.

## `search --json`

An array of catalog hits:

```json
[
  {
    "namespace": "security",
    "name": "owasp-review",
    "description": "Run an OWASP top-10 sweep.",
    "owner_team": "appsec",
    "tags": ["owasp", "security"],
    "latest_version": "1.2.0",
    "latest_hash": "abc...",
    "scan_status": "pass"
  }
]
```

## `config list --json`

A flat object keyed by configuration key:

```json
{
  "registry.url": "https://reg.internal/...",
  "registry.branch": "main",
  "defaults.scope": "auto",
  "cache.dir": ""
}
```

## Exit codes

All commands return one of the documented exit codes. Scripts should branch
on these rather than parsing error text.

| Code | Meaning                                                       |
| ---- | ------------------------------------------------------------- |
| 0    | success                                                       |
| 1    | generic failure (unexpected error)                            |
| 2    | invalid usage (bad command-line arguments, unknown config key) |
| 3    | registry unreachable                                          |
| 4    | portability lint failed                                       |
| 5    | no agents detected (or none compatible with the requested skill) |
| 6    | filesystem permission denied                                  |
