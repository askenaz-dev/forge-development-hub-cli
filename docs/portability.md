# Portability reference

A skill is either **portable** (works across all four AI agents) or
**non-portable** (declares an explicit `compatibility` list). The
portability lint enforces the distinction at install time and is the same
engine the CI publish flow uses, so a skill that passes locally publishes
without surprises.

## Declaration

```yaml
---
name: my-skill
description: short, action-oriented summary
portable: true            # default; can be omitted
license: MIT              # optional, allowed
metadata:                 # optional, allowed
  author: team-x
---

(body — portable prose, no agent-specific syntax)
```

For non-portable skills, declare the agents the skill targets:

```yaml
---
name: claude-only-skill
description: uses Claude-only frontmatter and substitutions
portable: false
compatibility:
  - claude-code
allowed-tools: Read Grep
---

(body may use $ARGUMENTS, !`shell`, ${CLAUDE_SKILL_DIR}, etc.)
```

## Rule catalogue

The lint reports a Finding for every violation, each with a stable rule
ID, a file path, and (where applicable) a line number.

### Frontmatter rules

| ID       | Rule                                                                                                                                  |
| -------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| PORT100  | Portable frontmatter contains a key outside the allowlist: `name`, `description`, `license`, `metadata`, `compatibility`, `portable`, `installed_from`. |
| PORT400  | `portable: false` declared without a non-empty `compatibility` list.                                                                  |
| PORT401  | A `compatibility` entry references an agent that is not present in the active adapter map.                                            |

### Body-substitution rules

These trigger on body text in portable skills. The conservative position is intentional for v1: if a token appears anywhere in the body — including inside fenced code blocks — the lint fails.

| ID       | Rule                                                                                                                          |
| -------- | ----------------------------------------------------------------------------------------------------------------------------- |
| PORT200  | Body contains `$ARGUMENTS` or `$ARGUMENTS[N]`.                                                                                |
| PORT201  | Body contains `${CLAUDE_SESSION_ID}`.                                                                                         |
| PORT202  | Body contains `${CLAUDE_SKILL_DIR}`.                                                                                          |
| PORT203  | Body contains `$0`..`$9` (positional shorthand). Currency-style `$0.00` is safely excluded — the lint requires a non-digit, non-dot follower. |

### Dynamic-context-injection rules

Claude Code's `!`cmd`` inline syntax and ```!` fenced blocks are only
recognized in Claude. Any of them in a portable body fails the lint.

| ID       | Rule                                                                          |
| -------- | ----------------------------------------------------------------------------- |
| PORT300  | Body contains the inline form: `!` followed by a backtick string, at start of line or after whitespace. |
| PORT301  | Body contains a fenced block opened with ` ```! `.                                |

## Examples

### A portable skill — passes

```yaml
---
name: code-review-standard
description: Run a standardized code review pass.
license: MIT
metadata:
  author: dx-platform
---

When reviewing a change, focus on:
- readability of the diff
- presence of tests for new behavior
- whether documentation needs updating
```

### A non-portable Claude-only skill — passes

```yaml
---
name: smart-deploy
description: Stage and ship a service via Claude Code's tools.
portable: false
compatibility:
  - claude-code
allowed-tools: Bash(git *) Bash(kubectl *)
disable-model-invocation: true
---

Deploy $ARGUMENTS to production:
1. Run the test suite.
2. Build the artifact.
3. Push to the cluster.
```

### A portable skill with leakage — fails

```yaml
---
name: leaky
description: looks portable but uses a Claude-only token
---

Run task $ARGUMENTS and write the result.
```

→ PORT200 at SKILL.md:6.

## Authoring guidance

- Default to **portable** unless the skill genuinely needs an agent-only
  feature.
- If documenting agent-specific syntax in a portable skill is unavoidable,
  the documentation belongs in a `references/` markdown file (which the
  lint does not scan), not in `SKILL.md`.
- The lint engine is exposed as a Go library at `pkg/portability` so future
  work (CI publish gates, IDE integrations, the broader scan engine in
  `scan-gate`) call exactly the same rules.
