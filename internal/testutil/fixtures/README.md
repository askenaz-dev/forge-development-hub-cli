# Test fixtures

Static skill bundles consumed by the installer test suite.

| Fixture                          | Purpose                                                                                                                |
| -------------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `portable-skill/`                | Minimum valid portable SKILL.md. Lint must pass.                                                                       |
| `claude-only-skill/`             | Non-portable Claude-only skill (`portable: false`, `compatibility: [claude-code]`). Installer must refuse other agents. |
| `portable-with-claude-leakage/`  | Portable skill that uses `$ARGUMENTS` in the body. Lint must flag PORT200.                                              |
| `with-scripts/`                  | Bundle with `scripts/` and `references/`. Tests subdirectory preservation and exec-bit handling.                       |

These fixtures complement the dynamic registry builder in
`internal/testutil/regbuilder.go`. Tests that need a tweak per case use
the dynamic builder; tests that benefit from a real on-disk reference
shape use these directories.
