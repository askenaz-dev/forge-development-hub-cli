---
name: portable-with-claude-leakage
description: A portable skill that accidentally leaks Claude-only syntax. Must fail the portability lint.
---

# Portability lint canary

This skill is declared portable (default) but contains a Claude-only token
in its body. The portability lint MUST flag PORT200 on the next line:

Run task $ARGUMENTS now.
