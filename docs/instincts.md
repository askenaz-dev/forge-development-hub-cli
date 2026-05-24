# Instincts

A bottom-up knowledge loop. You — the developer — capture short domain notes ("instincts") on the fly. Teammates trade bundles. Once enough instincts converge on the same pattern, an admin runs `fdh evolve` to clusterize them into a draft skill that lands in the hub via PR.

> **TL;DR**
>
> ```sh
> fdh instinct capture                       # write one
> fdh instinct list                          # see yours
> fdh instinct export team-bundle.tar.gz --all   # share
> fdh instinct import their-bundle.tar.gz    # collect
> fdh evolve                                  # admin: turn instincts into draft skills
> ```

## Why this exists

The hub catalog (skills, rules, agents, hooks) is top-down: the dx-platform team writes them, devs consume them. That scales linearly with the team. The instincts loop adds a bottom-up channel: each of N devs contributes domain knowledge, dx-platform curates. The catalog scales with the *use* of the tool, not just with platform capacity.

Concretely, a senior backend engineer who knows "always check OTel trace IDs when refactoring services" captures that as an instinct in 30 seconds. Six weeks later, dx-platform notices three similar instincts from different teams and runs `fdh evolve`, which generates a draft skill that distills the pattern. The admin curates, opens a PR to the hub, and now every backend dev gets the rule.

## Format

An instinct is a YAML+markdown file at `~/.fdh/instincts/<ULID>.yaml`:

```yaml
---
id: 01HXY7K2QZ3M5R9TPVNBJ8D6F4         # ULID; lexicographically sortable by capture time
title: "When refactoring Go services, verify OTel trace IDs are still propagated"
confidence: 0.8                          # 0.0–1.0 (manual; guide below)
domain: backend-services-go              # free-form kebab-case taxonomy
captured_by: dev@forge.com           # from ~/.fdh/config.yaml or FDH_USER_EMAIL
captured_at: 2026-05-23T10:15:00Z
context:
  project_hint: "checkout-service"       # basename of cwd at capture
  hub_commit: abc123def                  # from .fdh/lock.yaml if present
  triggers:
    - "user prompt mentioned 'refactor' AND 'service'"
tags: [go, trace, observability, refactor]
related_skills: [code-review]            # optional cross-refs
---

This is the markdown body. Free-form. Explain the pattern, the why,
the contraexamples, when NOT to apply.

## Context
What was happening when you noticed this…

## The idea
What to do, concretely…

## When NOT to apply
Edge cases…
```

## Confidence scoring (manual)

| Confidence | Meaning |
|---|---|
| `0.2–0.4` | First time you saw the pattern; could be a one-off |
| `0.5–0.7` | Observed in 3+ situations across projects; pattern feels real |
| `0.8–0.95` | Universal within the domain you wrote it for |
| `1.0` | Reserved for things you'd argue at a design review |

`fdh evolve` skips clusters with avg confidence below `--min-avg-confidence` (default 0.6). Don't inflate.

## Commands

### Capture

Interactive (in a TTY, with no flags):

```sh
fdh instinct capture
```

Walks you through title → domain → confidence → tags, then opens `$EDITOR` for the body. The disclaimer reminds you: never paste secrets — this file may be shared.

Flag-driven (CI, scripts):

```sh
fdh instinct capture \
  --title "When refactoring Go services, verify OTel trace IDs" \
  --domain backend-services-go \
  --confidence 0.7 \
  --tags go,trace,observability \
  --body-file ./body.md
```

Auto-populated from your environment:
- `captured_by` from `FDH_USER_EMAIL` env or `user.email` config.
- `context.project_hint` from `basename(cwd)`.
- `context.hub_commit` from the active project's `.fdh/lock.yaml` if present.

### List, show, edit, delete

```sh
fdh instinct list                                       # table
fdh instinct list --json                                # machine-readable
fdh instinct list --domain backend-services-go \
                   --confidence-min 0.5 --tag go        # filter
fdh instinct show 01HXY7K2                              # prefix match, prints full file
fdh instinct edit 01HXY7K2                              # opens in $EDITOR; re-validates on save
fdh instinct delete 01HXY7K2 --yes                      # delete with confirmation
```

ID prefixes resolve to a unique match or list candidates.

### Export / import

```sh
# Export a bundle (format inferred from extension)
fdh instinct export my-bundle.tar.gz --all
fdh instinct export team-pattern.yaml --domain backend-services-go --confidence-min 0.6

# Import a bundle (dedup by body hash; conflicts on same-ID-different-body fail)
fdh instinct import received-bundle.tar.gz
fdh instinct import their-bundle.yaml --dry-run    # preview without writing
```

Safety: `export` runs a built-in secrets scan (AWS keys, GitHub tokens, JWTs, URLs with embedded creds) before writing the bundle. If a finding shows up, the export aborts. Use `--no-scan` only when you've manually reviewed.

### Evolve (admin)

```sh
fdh evolve                                              # cluster local instincts
fdh evolve --from team-bundle.tar.gz                    # cluster a teammate's bundle
fdh evolve --from team-bundle.tar.gz --include-local    # union
fdh evolve --min-cluster-size 2 --min-avg-confidence 0.5  # tweak thresholds
```

Output: one `./fdh-evolve-output/<slug>/SKILL.md` draft per qualifying cluster. Each draft:

- Starts with a `> ⚠️ DRAFT` banner. **The hub's CI blocks PRs that still contain this banner** — admins must curate and remove it.
- Has partial frontmatter (`name`, `kind: skill`, `tags`) and TODO placeholders for `description`, `owner_team`, `agents_supported`.
- Has a `## Sourced from` section listing the cluster's source IDs + titles + the exact `fdh evolve` command that produced the draft, for traceability.
- Has placeholder sections (`## Purpose`, `## Rules`, `## How to apply`) with TODOs for the admin to fill.
- Has a collapsible `<details>` block with the verbatim source bodies — useful while curating, remove before merging.

Clustering is **deterministic, rule-based** (Jaccard over tags + title keywords, with English+Spanish stopwords). No LLMs in v1. Same input always produces the same output (modulo the timestamp in the banner).

## Privacy

- **The body is whatever you write.** Capture is never automatic in v1.
- The auto-populated context (`project_hint`, `hub_commit`, `triggers`) is synthetic metadata, never raw project content.
- `fdh instinct export` runs a built-in secrets scan before generating the bundle.
- Files live in `~/.fdh/instincts/` with `0700` permissions on Unix (ACL-equivalent on Windows).
- Encryption at rest is out of scope for v1. If security policy requires it, file an issue.

## Where instincts end up

```
            ┌──────────────────────────────────────┐
            │  ~/.fdh/instincts/<ULID>.yaml         │
            │  (per-dev local; not committed)      │
            └────────────────┬─────────────────────┘
                             │ export
                             ▼
            ┌──────────────────────────────────────┐
            │  team-bundle.tar.gz                   │
            │  (Slack / shared drive / artifact)   │
            └────────────────┬─────────────────────┘
                             │ admin imports & evolves
                             ▼
            ┌──────────────────────────────────────┐
            │  ./fdh-evolve-output/<slug>/SKILL.md  │
            │  (draft for human curation)          │
            └────────────────┬─────────────────────┘
                             │ admin curates + PR
                             ▼
            ┌──────────────────────────────────────┐
            │  hub/registry.yaml + skills/<slug>/   │
            │  (everyone gets it via fdh install)  │
            └──────────────────────────────────────┘
```

## What's not in v1 (deferred)

- **No backend / sync service.** Bundles travel manually. The future `add-instinct-sync-service` change adds a push/pull HTTP server.
- **No auto-capture.** Manual only. Future `add-instinct-auto-capture-via-hooks` integrates with Stop-phase hooks so each session can emit one or more instincts automatically.
- **No LLM clustering.** Rule-based only. Future `evolve-instincts-with-llm` uses embeddings for semantic similarity (catches paraphrased patterns the Jaccard misses).
- **No peer voting.** Future `add-instinct-review-loop` lets devs upvote / downvote others' instincts before admin curation.
- **No encryption at rest.** Future change if security requires.

All four are additive — the file format and command contracts won't change, just gain new abilities.

## Tutorial: capture your first instinct in 3 minutes

```sh
# 1. Make sure your identity is set.
export FDH_USER_EMAIL="${USER}@forge.com"

# 2. Capture interactively. Use a real pattern from today's work.
fdh instinct capture
#   Title: e.g. "Before merging API changes, always check OpenAPI doc drift"
#   Domain: e.g. "backend-services-api"
#   Confidence: 0.7
#   Tags: api, openapi, review
#   (opens $EDITOR; write a few paragraphs explaining the pattern)

# 3. Verify it landed.
fdh instinct list

# 4. Read it back.
fdh instinct show <first-8-chars-of-id>

# 5. (Optional) bundle it and share with your team.
fdh instinct export ./my-first.yaml --all
```

That's it. Repeat over the next weeks; export periodically; let admin handle clustering.
