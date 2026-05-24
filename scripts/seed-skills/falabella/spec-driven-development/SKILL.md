---
name: spec-driven-development
description: Drive non-trivial changes through a four-phase spec-driven flow — explore, propose, apply, archive. Use when the user wants to introduce SDD discipline to a team, kick off a new change formally, or align on requirements before any implementation work begins.
license: MIT
metadata:
  author: dx-platform
  sdlc_phase: development
  methodology: spec-driven
  references:
    - https://openspec.dev/
    - https://github.com/forge/forge-development-hub
---

# Spec-driven development at forge

forge uses spec-driven development (SDD) for every non-trivial change.
The flow is the OpenSpec workflow hosted at the `forge-development-hub`
repository. This skill explains when to use it, the four phases, the
artifact set, and the conventions that bite.

## What "non-trivial" means here

Use SDD for any change that crosses one or more of:

- adds or modifies a capability that other teams will rely on,
- changes a public API (REST, gRPC, GraphQL, message schema),
- introduces a new technology or dependency that needs platform approval,
- modifies behavior in code that already has an archived spec,
- spans more than ~one engineering day of work or more than one engineer.

For typo fixes, dependency bumps, and one-line bug fixes, skip SDD and
ship a normal PR.

## The four phases

1. **Explore** — thinking partner mode. Read code, ask questions, sketch
   options. Produce artifacts only when an idea crystallises. Never write
   application code in this phase.
2. **Propose** — scaffold `openspec/changes/<name>/` and author the four
   artifacts (proposal, design, specs, tasks). The change name is
   kebab-case and describes the outcome, not the activity (e.g.
   `add-product-catalog-api`, not `change-stuff`).
3. **Apply** — work the tasks top-down. Flip `- [ ]` to `- [x]` the
   moment a task is complete, not in a batch at the end. Pause on
   ambiguity, on errors, and when implementation reveals a design issue
   that calls for updating the artifacts.
4. **Archive** — move the change directory into
   `openspec/changes/archive/YYYY-MM-DD-<name>/`. Before archiving, sync
   any delta specs under `openspec/changes/<name>/specs/` into the
   canonical `openspec/specs/<capability>/spec.md`.

## The artifact set

For the `spec-driven` schema, every change has at minimum:

- **proposal.md** — the WHY (motivation, what changes at a behavioural
  level, which capabilities are new vs. modified, impact). Keep under
  two pages. Identify every capability the change touches.
- **design.md** — the HOW. Context, goals/non-goals, decisions with
  rationale and alternatives, risks/trade-offs, migration plan, open
  questions.
- **specs/<capability>/spec.md** — one file per capability listed in the
  proposal. Each requirement uses `### Requirement: <name>` and is
  followed by one or more `#### Scenario:` blocks with WHEN/THEN form.
  Every requirement must have at least one scenario. Scenarios are the
  testable contract — each maps to a potential test.
- **tasks.md** — the implementation checklist. Tasks are checkboxes
  grouped under numbered headings. The apply phase reads these and
  tracks progress by flipping checkboxes.

The CLI prints what each artifact needs:

```
openspec instructions proposal --change "<name>" --json
openspec instructions design --change "<name>" --json
openspec instructions specs --change "<name>" --json
openspec instructions tasks --change "<name>" --json
```

Three fields in the instructions output matter:

- `template` — the structure to write into the file.
- `instruction` — schema-specific guidance for what content goes there.
- `context` and `rules` — constraints on the AUTHOR, not content. Do
  not copy `<context>`, `<rules>`, or `<project_context>` blocks into the
  artifact file.

## Conventions that bite

- **Scenarios MUST use exactly four hashtags** (`#### Scenario:`). Three
  hashtags or bullet-style "Scenarios:" headers fail silently — the
  archive step will leave them out of the canonical spec.
- **Use SHALL / MUST for normative requirements.** Avoid should/may
  unless the requirement is genuinely optional. Reviewers will challenge
  vague language.
- **One capability per file under specs/`<capability>`/spec.md.** Do not
  combine multiple capabilities into a single spec file even if they're
  closely related — the archive step keys off the directory structure.
- **MODIFIED requirements must include the full updated text.** Do not
  use partial diffs; the archive step replaces the previous text wholesale.
- **REMOVED requirements need both Reason and Migration.** Without these
  the spec sync refuses to apply.
- **Open questions block apply on irreversible decisions.** If a question
  is genuinely undecided AND changing it later would require a separate
  change, pause apply and ask. If a sensible default exists, document
  it in design.md and proceed.

## Anti-patterns

- "I'll just write the code and document later." → No. SDD's value is
  the conversation before the code. Reverse the order and you've added
  ceremony without insight.
- One mega-change that touches everything. → Split. Each change should
  be archivable independently. If a change has more than ~15 tasks or
  more than 4 capabilities, it's two changes wearing one hat.
- Designing in proposal.md. → proposal.md is the WHY; design.md is the
  HOW. Crossing them turns reviews into a slog because the reader can't
  separate "do we agree on the problem?" from "do we agree on the approach?"

## Tooling

- The `openspec` CLI manages the workflow locally.
- `/opsx:explore`, `/opsx:propose`, `/opsx:apply`, `/opsx:archive` are
  the standard skills wrapping each phase across AI agents (Claude
  Code, Codex, Copilot, OpenCode).
- The `openspec validate <change>` command checks structural integrity
  before archive.
- `openspec archive <change>` is the only supported way to retire a
  change — it does the spec sync atomically with the directory move.

## When the workflow itself needs to change

Treat the openspec hub like any other repo: open a change against IT
(meta-SDD). The hub's `CLAUDE.md` documents the conventions; updating
those is itself a spec change.
