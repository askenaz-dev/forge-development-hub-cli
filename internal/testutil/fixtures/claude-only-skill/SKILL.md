---
name: claude-only-skill
description: A non-portable Claude-only skill fixture. Installs only to claude-code.
portable: false
compatibility:
  - claude-code
allowed-tools: Read Grep
---

# Claude-only fixture

This file uses Claude-only features and is declared `portable: false`.
The installer must refuse to write it to any agent not listed in
`compatibility`. The portability lint must not flag the Claude-only
features in this skill because the skill is non-portable.

Run on $ARGUMENTS in ${CLAUDE_SKILL_DIR}.
