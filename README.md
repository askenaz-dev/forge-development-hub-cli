# fdh

Cross-platform CLI that installs spec-compliant [Agent Skills](https://agentskills.io) to four AI coding agents — Claude Code, GitHub Copilot, OpenAI Codex, OpenCode — from a shared Git-backed skill registry.

This repository holds the **implementation**. The **specification** that governs every requirement here lives in the Falabella OpenSpec hub:

- Hub: `falabella-development-hub`
- Change: [`installer-core`](../falabella-development-hub/openspec/changes/installer-core/) (in progress)
- Specs (after archive): `falabella-development-hub/openspec/specs/`

If a behavior of the installer disagrees with the spec, the spec wins. Open a change in the hub to alter requirements; do not change behavior here without a corresponding spec update.

## Status

Pre-release. Pilot target: 30 Falabella developers across macOS, Linux, and Windows. See [`docs/quickstart.md`](docs/quickstart.md) once available.

## Layout

```
cmd/fdh/   # main + root cobra command
internal/cli/              # one file per CLI subcommand
pkg/registry/              # Registry interface + GitRegistry implementation
pkg/adapters/              # manifest-driven agent path map
pkg/bundle/                # skill bundle parsing, validation, canonical hash
pkg/portability/           # portability lint engine
pkg/provenance/            # .skill-meta.yaml sidecar + frontmatter breadcrumb
internal/testutil/         # shared fixtures and helpers
docs/                      # quickstart, release notes, adapter reference
```

## Build

```
task build      # build the binary for the current host
task test       # unit + integration tests
task lint       # golangci-lint
task e2e        # end-to-end test against fixture registry
task release    # produce all five platform archives
```

[Task](https://taskfile.dev) is used as the build runner: a single cross-platform binary, identical syntax on macOS/Linux/Windows, no `make` dependency.

## License

MIT — see [LICENSE](LICENSE).

## Contributing

Contributions land via PR against `main`. CI must pass on macOS, Linux, and Windows before merge. Any change to runtime behavior requires a corresponding OpenSpec change in the hub repository.
