# @forge/fdh

npm wrapper for the [forge Development Hub](https://github.com/forge/fdh) CLI. The published package contains a thin TypeScript/JavaScript layer that downloads the right Go binary for the developer's platform at install time and dispatches to it at runtime.

```bash
npx @forge/fdh init           # zero-install
npm i -g @forge/fdh           # persistent
```

## How it works

```
       ┌────────────────────────────────────────────┐
       │  npm i (or npx) @forge/fdh             │
       └─────────────────┬──────────────────────────┘
                         │
                         ▼
       ┌────────────────────────────────────────────┐
       │  postinstall.js (sync)                      │
       │  ─────────────────                          │
       │  1. detect process.platform + arch         │
       │  2. resolve $FDH_PKG_HOST/fdh/<ver>/...     │
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
├── package.json           # name=@forge/fdh, bin maps fdh + forge-installer
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

The npm package version always equals the underlying Go binary version. A single `git tag vX.Y.Z` on the Go repo triggers an atomic release: Go cross-compile → upload binaries + SHA-256 + manifest → `npm version` → `npm publish`. No independent cycles.

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
FDH_PKG_HOST=pkg.forge.internal node dist/postinstall.js
./dist/cli.js --version
```

## See also

- Hub-side spec: `openspec/specs/fdh-npm-wrapper/spec.md` in the forge-development-hub.
- Hub-side spec: `openspec/specs/fdh-cli-distribution/spec.md` (channel ordering).
- Release process: `../docs/release-process.md`.
- Troubleshooting (proxies, cert inspection): `../docs/troubleshooting.md`.
