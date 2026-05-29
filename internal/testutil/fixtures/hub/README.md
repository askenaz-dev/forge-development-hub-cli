# Hub wire-protocol test fixture

Deterministic mini-hub used by `internal/portalapi/wire_handlers_test.go` to
exercise the HTTP wire-protocol handlers (`/v1/index.json`, manifest, bundle
tarball, sha256 sidecar).

Layout mirrors `forge-development-hub`:

```
hub/registry.yaml       v2 schema_version catalog with 4 components
skills/test-skill/      skill bundle + nested reference
rules/test-rule/        rule bundle
agents/test-agent/      agent bundle
hooks/test-hook/        hook bundle
```

## Why SKILL.md in non-skill kinds

The portal-api currently uses `pkg/bundle.Load`, which expects a `SKILL.md`
entrypoint regardless of kind. Naming every bundle's entrypoint `SKILL.md`
keeps the loader uniform; when the hub formalizes per-kind entrypoints
(RULE.md / AGENT.md / HOOK.md), this fixture and the loader gain a kind
parameter together.

## Don't edit without updating tests

Several wire-handler tests assert specific golden values (component counts,
namespace constants, sidecar bytes). Changing any frontmatter or content
will break those tests in a useful way — investigate before deleting
fixtures.
