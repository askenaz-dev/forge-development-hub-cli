# Troubleshooting

Common issues installing or running `fdh`, ordered by frequency.

## Install via npm fails with `EAI_AGAIN` / `ECONNRESET` / proxy errors

The npm wrapper's postinstall downloads the Go binary from `${FDH_RELEASES_BASE}`. Behind a corporate firewall, you need a proxy. The wrapper honors (in priority order):

1. `npm_config_https_proxy` / `npm_config_proxy` — set by `.npmrc`
2. `HTTPS_PROXY` / `HTTP_PROXY` — env vars
3. `NO_PROXY` — opts hosts out of proxying (supports `*`, hostname, `.example.com`)

### Configure via `.npmrc`

```ini
# ~/.npmrc (or .npmrc at project root)
https-proxy=http://corp-proxy.askenaz.dev:8080
proxy=http://corp-proxy.askenaz.dev:8080
noproxy=.askenaz.dev,localhost
```

### Configure via env vars (transient)

```sh
export HTTPS_PROXY=http://corp-proxy.askenaz.dev:8080
export NO_PROXY=.askenaz.dev,localhost
npx @askenaz-dev/fdh init
```

```powershell
$env:HTTPS_PROXY = "http://corp-proxy.askenaz.dev:8080"
$env:NO_PROXY = ".askenaz.dev,localhost"
npx @askenaz-dev/fdh init
```

## Install fails with `unable to verify the first certificate` / SSL errors

Your corporate proxy is doing TLS inspection — it re-signs HTTPS connections with a corporate-issued certificate. Node needs to trust that CA.

```sh
# Set NODE_EXTRA_CA_CERTS to your corporate CA bundle (often `.pem` or `.crt`).
export NODE_EXTRA_CA_CERTS=/path/to/forge-corporate-ca.pem
npx @askenaz-dev/fdh init
```

```powershell
$env:NODE_EXTRA_CA_CERTS = "C:\path\to\forge-corporate-ca.pem"
npx @askenaz-dev/fdh init
```

Permanent fix: add `NODE_EXTRA_CA_CERTS` to your shell profile. Some IT departments also configure `NPM_CONFIG_CAFILE`:

```ini
# ~/.npmrc
cafile=/path/to/forge-corporate-ca.pem
```

## `fdh: binary not found at <path>; run 'npm rebuild @askenaz-dev/fdh' to repair`

The postinstall script either skipped or failed. Repair:

```sh
npm rebuild @askenaz-dev/fdh   # for npm-installed
pnpm rebuild @askenaz-dev/fdh  # for pnpm
yarn rebuild @askenaz-dev/fdh  # for yarn (where supported)
```

If `rebuild` fails too, check that:

- `tar` is available on `PATH` (required to extract the binary tarball; built into Windows 10 1809+).
- `FDH_RELEASES_BASE` is set or reachable (`echo $FDH_RELEASES_BASE` or `echo $env:FDH_RELEASES_BASE`).
- Your proxy / cert config (above) is correct.

As a last resort, fall back to `install.sh` (POSIX) or `install.ps1` (Windows) — see [quickstart.md](./quickstart.md#fallback--posix--powershell-one-liner).

## `fdh: no prebuilt binary for <platform>-<arch>`

The matrix we ship covers: `darwin-arm64`, `darwin-amd64`, `linux-arm64`, `linux-amd64`, `windows-amd64`. If you need an additional target (e.g., `windows-arm64`, `linux-mips`, FreeBSD), contact `maintainers` or build from source:

```sh
git clone https://github.com/forge/fdh
cd fdh
go build -o ~/bin/fdh ./cmd/fdh
~/bin/fdh --version
```

## Package manager edge cases

### pnpm

`pnpm` installs to a content-addressable store and symlinks into `node_modules/.pnpm/`. The wrapper handles this correctly — the binary path is resolved relative to `import.meta.url` so symlinking doesn't break it. If `pnpm rebuild @askenaz-dev/fdh` doesn't work, try `pnpm install --shamefully-hoist`.

### Yarn classic (1.x)

Yarn classic's `--ignore-scripts` is global, not per-package. If you set it globally to avoid scripts from other packages, our postinstall is skipped too — set `FDH_SKIP_POSTINSTALL=0` (default) and run `yarn add @askenaz-dev/fdh` without `--ignore-scripts` once.

### Yarn berry (2+) / PnP

We haven't validated yarn berry's Plug'n'Play mode. If you're on Yarn berry, use the `node-modules` linker:

```ini
# .yarnrc.yml
nodeLinker: node-modules
```

### Bun

Bun is detected but not officially supported. If `bun add @askenaz-dev/fdh` fails on the postinstall, fall back to `npm i -g @askenaz-dev/fdh` for now and file an issue.

## Cache miss / `cache miss (sha256 differs)`

Normal during version transitions: the binary you have doesn't match the version your `package.json` declares. The postinstall re-downloads automatically. If it happens repeatedly on the *same* version, your CDN or proxy may be serving stale content — try `NO_PROXY=*` once to bypass.

## `FDH_SKIP_POSTINSTALL`

Set `FDH_SKIP_POSTINSTALL=1` to skip the binary download entirely. Useful for:

- Docker base images that build their own `fdh` from source.
- CI matrices that test the wrapper's TypeScript without needing the binary.
- Local dev when you've manually placed an `fdh` binary on PATH.

The wrapper will then look for `fdh` (or `fdh.exe` on Windows) anywhere on your `PATH`, not just inside the package.

## HTTP registry: `401 Unauthorized` from `fdh search` / `install` / `doctor`

The registry you're hitting requires authentication and the binary isn't sending any. The HTTP transport supports three modes, applied in order: bearer → basic → mTLS (client certs).

```sh
# Bearer token (most common)
fdh config set registry.http.auth.bearer "$REGISTRY_TOKEN"
# Or via env (preferred in CI):
export FDH_REGISTRY_HTTP_BEARER="$REGISTRY_TOKEN"
fdh search owasp
```

```sh
# Basic auth
fdh config set registry.http.auth.basic.user alice
fdh config set registry.http.auth.basic.pass s3cret
# Or via env:
export FDH_REGISTRY_HTTP_BASIC_USER=alice
export FDH_REGISTRY_HTTP_BASIC_PASS=s3cret
```

```sh
# mTLS — both files must be PEM and live at absolute paths
fdh config set registry.http.auth.client_cert /etc/ssl/cli.crt
fdh config set registry.http.auth.client_key  /etc/ssl/cli.key
```

Confirm what the binary is sending with `fdh -v doctor` — the verbose log includes the auth method and the request URL (token values are never printed).

## HTTP registry: mTLS handshake failure

Symptoms: `registry unreachable: ... bad certificate` or `tls: handshake failure`.

Common causes:

- **Cert path doesn't exist.** Both `client_cert` and `client_key` must be absolute paths to existing PEM files. Use `fdh config get registry.http.auth.client_cert` to confirm; the path is read literally with no shell expansion.
- **Cert / key mismatch.** The public cert in `client_cert` must be paired with the private key in `client_key`. Quick check: `openssl x509 -noout -modulus -in $cert | openssl md5` vs. `openssl rsa -noout -modulus -in $key | openssl md5` — the MD5s must match.
- **CA the registry server uses isn't trusted by the host.** Add the registry's CA to the system trust store (macOS Keychain, Linux `/etc/ssl/certs`, Windows certificate manager).
- **TLS 1.2 or older required.** The HTTP transport requires TLS 1.2+. If the registry is on an old cipher suite, upgrade the server.

## HTTP registry: `cannot auto-detect registry transport from "<url>"`

`registry.kind=auto` couldn't classify the URL. Either:

- The URL scheme isn't `http://`, `https://`, `git@`, `ssh://`, `git://`, nor ends in `.git`.
- The URL is malformed.

Fix by setting `registry.kind` explicitly:

```sh
fdh config set registry.kind http   # or git
```

`registry.kind` always wins over auto-detection.

## Reporting bugs

When opening an issue, include the output of:

```sh
fdh --version
node --version
npm --version    # or pnpm --version / yarn --version
echo "$(npm config get https-proxy) | $HTTPS_PROXY | $NO_PROXY"
uname -a         # or `systeminfo` on Windows
```

…plus the exact error message. The npm wrapper's postinstall prints actionable hints; copy those verbatim.

## See also

- [`quickstart.md`](./quickstart.md) — initial install + first skill.
- [`install.md`](./install.md) — per-channel deep dive.
- [`release-process.md`](./release-process.md) — how versions are cut.
- [`KNOWN_ISSUES.md`](./KNOWN_ISSUES.md) — currently tracked issues.
