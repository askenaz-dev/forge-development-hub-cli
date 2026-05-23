---
name: portable-skill
description: A reference portable skill used by the fixture suite. Demonstrates the minimum valid SKILL.md.
license: MIT
metadata:
  author: testutil
  category: testing
---

# Portable skill fixture

This file is the canonical example of a portable skill. The portability lint
must accept this file in any test that targets it.

## Body

Skills should use plain prose with no agent-specific tokens. Avoid `dollar-ARG`
patterns, dynamic context injection, and any frontmatter key outside the
agentskills.io standard intersection.
