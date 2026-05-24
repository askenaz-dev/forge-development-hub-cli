# `fdh validate-registry`

Validate the hub's `skills/registry.yaml` against the
[`hub-skills-registry`](../../forge-development-hub/openspec/specs/hub-skills-registry/spec.md)
spec rules. Designed for two audiences:

- **Hub maintainers** running locally before opening a PR.
- **CI pipelines** replacing the previous Python validator
  (`tools/validate-registry.py` in `forge-development-hub`).

## Usage

```sh
# Validate the registry in the current directory.
fdh validate-registry

# Validate a specific clone of the hub.
fdh validate-registry /path/to/forge-development-hub

# Machine-readable output for CI.
fdh validate-registry /path/to/hub --json
```

## What it checks

| Rule | Description | Error code |
|---|---|---|
| `schema-version` | `schema_version` matches a supported value (currently `1`). | `schema-version` |
| `name-required` | Every skill entry has a `name`. | `name-required` |
| `name-kebab-case` | Names use lowercase letters, digits, single dashes (e.g. `design-system`). | `name-kebab-case` |
| `unique-name` | No two entries share the same `name`. | `unique-name` |
| `path-required` | Every entry declares a `path`. | `path-required` |
| `path-exists` | The declared path exists on disk and is a directory. | `path-exists` |
| `agents-supported-nonempty` | `agents_supported` is a non-empty list. | `agents-supported-nonempty` |
| `semver` | `min_fdh_version`, when set, parses as semver. | `semver` |
| `no-orphans` | Every `skills/<X>/` directory under the repo has a corresponding registry entry. | `no-orphans` |
| `yaml-syntax` | The registry file is well-formed YAML. | `yaml-syntax` |

The exit code is `0` when valid, `7` (`ExitValidation`) when at least one
rule fails.

## JSON output

The shape is stable (additive-only contract):

```json
{
  "ok": false,
  "repo_root": "/abs/path/to/hub",
  "errors": [
    {
      "rule": "unique-name",
      "message": "duplicate name \"design-system\" (first seen at index 0)",
      "location": "registry.yaml#/skills/2 (design-system)"
    },
    {
      "rule": "path-exists",
      "message": "path \"skills/legacy-design\" does not exist on disk",
      "location": "registry.yaml#/skills/4 (legacy-design)"
    }
  ]
}
```

When validation succeeds, `errors` is the empty array `[]`. Future fields
may be added to the top-level object; existing fields will never change
type or semantics.

## CI integration

Add a step to the hub's GitHub Actions workflow:

```yaml
- name: Validate skills/registry.yaml
  run: |
    fdh validate-registry .
    # On failure, exit code 7 surfaces; CI fails the job.
```

For richer reporting, pipe `--json` through `jq` and assert on rule
identifiers:

```yaml
- name: Validate (with assertions)
  run: |
    out="$(fdh validate-registry . --json)"
    code=$?
    echo "$out" | jq .
    if [ $code -ne 0 ]; then
      echo "$out" | jq '.errors[] | "[\(.rule)] \(.message) @ \(.location)"' -r
      exit 1
    fi
```

## Pre-commit hook

The check is fast (parse + filesystem stat) so it's a good fit for a
pre-commit hook in the hub:

```sh
# .git/hooks/pre-commit  (or .pre-commit-config.yaml entry)
#!/bin/sh
fdh validate-registry .
```

## Migration from the Python validator

The hub previously shipped `tools/validate-registry.py`. The Go
implementation:

- Same set of rules, same exit-code semantics.
- Same JSON output shape (additive — the Python version's keys map 1:1).
- Faster (no Python interpreter startup; ~10× quicker on a cold cache).
- Single binary to install — no `pip install pyyaml` step.

To switch the hub's CI:

```yaml
# .github/workflows/validate-registry.yml — proposed
- uses: actions/checkout@v4
- name: Install fdh
  run: curl -fsSL https://${{ vars.FDH_PKG_HOST }}/fdh/install.sh | bash
- name: Validate registry
  run: fdh validate-registry .
```

The deprecation timeline lives in task 13.1 of the
`implement-cli-distribution-and-interactive-init` change. Once the hub
PR lands, `tools/validate-registry.py` can be deleted.

## Troubleshooting

| Error rule | Likely cause | Fix |
|---|---|---|
| `yaml-syntax` | Tab indentation, unbalanced quotes, missing colon. | Open the file in an editor that highlights YAML errors. |
| `path-exists` for an entry you just added | The entry was added before the directory was committed. | `git add skills/<name>/` and re-stage. |
| `no-orphans` for `skills/.DS_Store` or similar | A non-skill file leaked into the repo. | Add to `.gitignore` and remove. |
| `unique-name` after a rename | A new entry was added but the old one wasn't removed. | Delete the obsolete entry from the YAML. |
