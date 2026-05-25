# @askenaz-dev/fdh

npm wrapper for the [Forge Development Hub](https://github.com/askenaz-dev/forge-development-hub-cli) CLI. The published package contains a thin TypeScript/JavaScript layer that downloads the right Go binary for the developer's platform at install time and dispatches to it at runtime.

```bash
npx @askenaz-dev/fdh init           # zero-install
npm i -g @askenaz-dev/fdh           # persistent
```

## How it works

```
       ┌────────────────────────────────────────────┐
       │  npm i (or npx) @askenaz-dev/fdh           │
       └─────────────────┬──────────────────────────┘
                         │
                         ▼
       ┌────────────────────────────────────────────┐
       │  postinstall.js (sync)                      │
       │  ─────────────────                          │
       │  1. detect process.platform + arch          │
       │  2. resolve GitHub Releases asset URL       │
       │     (or FDH_RELEASES_BASE override)         │
       │  3. download tarball + SHA-256 (via proxy   │
       │     if configured)                          │
       │  4. verify SHA-256                          │
       │  5. extract to node_modules/.../bin/        │
       └─────────────────┬──────────────────────────┘
                         │
                         ▼
       ┌────────────────────────────────────────────┐
       │  user runs `fdh <cmd>`                      │
       │  npm bin shim → dist/cli.js (wrapper)       │
       │  wrapper spawn() bin/fdh<.exe> w/ argv      │
       │  exit code propagated                       │
       └────────────────────────────────────────────┘
```

## Repo layout

```
npm/
├── package.json           # name=@askenaz-dev/fdh, bin maps fdh + forge-installer
├── tsconfig.json          # strict TS 5+, ES2022, Node16 modules
├── vitest.config.ts       # tests in tests/*.test.ts
├── .npmignore             # only dist/ ships
├── src/
│   ├── lib.ts             # helpers: targets, proxy, download, sha256, extract
│   ├── cli.ts             # entry for `fdh` binary
│   ├── cli-alias.ts       # entry for `forge-installer` (deprecation alias)
│   └── postinstall.ts     # downloads the Go binary into bin/
├── scripts/
│   └── post-build-fixup.mjs   # adds shebang + chmod +x to dist/*.js
└── tests/
    ├── lib.test.ts
    ├── cli.test.ts
    └── postinstall.test.ts
```

## Zero runtime dependencies

The published tarball depends on `node >= 18` and that's it. The wrapper uses only Node stdlib:
- `node:fs`, `node:path` (filesystem)
- `node:https`, `node:url` (download)
- `node:crypto` (SHA-256)
- `node:child_process` (spawn the binary + delegate tar extraction)
- `node:os` (platform/arch detection)

System binaries required at install/runtime:
- `tar` (built into Windows 10 1809+; standard on macOS/Linux).

## Versioning

The npm package version always equals the underlying Go binary version. A single `git tag vX.Y.Z` on the repo triggers an atomic release: Go cross-compile → upload binaries + SHA-256 to GitHub Releases → `npm version` → `npm publish`. No independent cycles.

## Configuration

The postinstall script and CLI wrapper honor these env vars:

| Variable | Default | Purpose |
|----------|---------|---------|
| `FDH_RELEASES_BASE` | `https://github.com/askenaz-dev/forge-development-hub-cli/releases` | Base URL for binary downloads. Override to point at a private mirror following the same release-asset layout. |
| `FDH_SKIP_POSTINSTALL` | (unset) | Set to `1` or `true` to skip the postinstall binary download (useful in CI when you only need the wrapper). |
| `HTTPS_PROXY` / `HTTP_PROXY` / `NO_PROXY` | (env) | Standard proxy env vars. Also reads `npm_config_https_proxy` / `npm_config_proxy` from `.npmrc`. |
| `NODE_EXTRA_CA_CERTS` | (env) | Standard Node CA bundle override for corporate cert-inspection proxies. |

## Local development

```bash
cd C:/forge/fdh/npm

# Install dev deps
npm install --ignore-scripts   # skip postinstall on dev install

# Build (TypeScript → JS in dist/, adds shebang)
npm run build

# Run unit tests
npm test

# Smoke test the wrapper without publishing
node dist/postinstall.js
./dist/cli.js --version
```

## Publishing to an alternate registry

Default publish target is `https://registry.npmjs.org/` (public, set via `publishConfig.registry`). To publish to a private registry instead, override at publish time:

```bash
npm publish --registry https://npm.pkg.github.com/
# or
NPM_CONFIG_REGISTRY=https://npm.askenaz.dev/ npm publish
```

See `.npmrc.template` for the per-scope configuration pattern.

## See also

- Hub-side spec: `openspec/specs/fdh-npm-wrapper/spec.md` in the forge-development-hub.
- Hub-side spec: `openspec/specs/fdh-cli-distribution/spec.md` (channel ordering).
- Release process: `../docs/release-process.md`.
- Troubleshooting (proxies, cert inspection): `../docs/troubleshooting.md`.
