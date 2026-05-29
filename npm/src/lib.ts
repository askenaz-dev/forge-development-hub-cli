// Shared helpers used by postinstall + cli + tests.
// Zero runtime dependencies — only Node stdlib.

import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { promises as fs } from "node:fs";
import * as https from "node:https";
import * as http from "node:http";
import * as path from "node:path";
import { URL } from "node:url";

// -----------------------------------------------------------------------------
// Target resolution
// -----------------------------------------------------------------------------

/** Targets we cross-compile the Go binary for. Update if you add new GOOS/GOARCH. */
export const SUPPORTED_TARGETS = [
  "darwin-arm64",
  "darwin-amd64",
  "linux-arm64",
  "linux-amd64",
  "windows-amd64",
] as const;

export type SupportedTarget = (typeof SUPPORTED_TARGETS)[number];

/** Resolve the current process platform + arch to one of SUPPORTED_TARGETS, or throw. */
export function resolveTarget(
  platform: NodeJS.Platform = process.platform,
  arch: string = process.arch,
): SupportedTarget {
  const osMap: Record<string, string | undefined> = {
    darwin: "darwin",
    linux: "linux",
    win32: "windows",
  };
  const archMap: Record<string, string | undefined> = {
    arm64: "arm64",
    x64: "amd64",
  };
  const os = osMap[platform];
  const a = archMap[arch];
  if (!os || !a) {
    throw new TargetUnsupportedError(platform, arch);
  }
  const candidate = `${os}-${a}` as SupportedTarget;
  if (!(SUPPORTED_TARGETS as readonly string[]).includes(candidate)) {
    throw new TargetUnsupportedError(platform, arch);
  }
  return candidate;
}

/** Filename of the binary inside the extracted tarball. */
export function binaryFilename(target: SupportedTarget): string {
  return target.startsWith("windows") ? "fdh.exe" : "fdh";
}

export class TargetUnsupportedError extends Error {
  constructor(platform: string, arch: string) {
    super(
      `fdh: no prebuilt binary for ${platform}-${arch}. ` +
        `Supported: [${SUPPORTED_TARGETS.join(", ")}]. ` +
        `If you need this target, build from source ` +
        `(https://github.com/askenaz-dev/forge-development-hub-cli).`,
    );
    this.name = "TargetUnsupportedError";
  }
}

// -----------------------------------------------------------------------------
// Proxy resolution
// -----------------------------------------------------------------------------

/** Resolve the proxy URL to use for a given target URL. Honors NO_PROXY. */
export function resolveProxy(
  targetUrl: string,
  env: NodeJS.ProcessEnv = process.env,
): string | null {
  const target = new URL(targetUrl);

  // Check NO_PROXY first; covers `*`, full hostnames, suffix matches.
  const noProxyRaw = env.NO_PROXY ?? env.no_proxy ?? "";
  for (const entry of noProxyRaw.split(",").map((s) => s.trim()).filter(Boolean)) {
    if (entry === "*") return null;
    const norm = entry.startsWith(".") ? entry : "." + entry;
    if (target.hostname === entry || target.hostname.endsWith(norm)) {
      return null;
    }
  }

  // Cascade: npm config (set by npm/pnpm/yarn from .npmrc) → env vars.
  const httpsProxy =
    env.npm_config_https_proxy ??
    env.HTTPS_PROXY ??
    env.https_proxy ??
    null;
  const httpProxy =
    env.npm_config_proxy ?? env.HTTP_PROXY ?? env.http_proxy ?? null;

  return target.protocol === "https:" ? (httpsProxy ?? httpProxy) : httpProxy;
}

// -----------------------------------------------------------------------------
// Download
// -----------------------------------------------------------------------------

export interface DownloadOptions {
  /** Maximum redirects to follow. Default 5. */
  maxRedirects?: number;
  /** Request timeout (ms). Default 60_000. */
  timeoutMs?: number;
  /** Env to read proxy config from. Defaults to process.env. */
  env?: NodeJS.ProcessEnv;
  /**
   * Inject a request function for testing. Default uses Node's https.
   * Receives URL and headers, returns a Promise of Buffer (body) + statusCode + headers.
   */
  requestImpl?: (
    targetUrl: string,
    headers: Record<string, string>,
    proxy: string | null,
  ) => Promise<{ statusCode: number; headers: Record<string, string | string[] | undefined>; body: Buffer }>;
}

/** Download a URL to a Buffer. Follows redirects. Honors proxy + NO_PROXY. */
export async function download(
  url: string,
  opts: DownloadOptions = {},
): Promise<Buffer> {
  const maxRedirects = opts.maxRedirects ?? 5;
  const env = opts.env ?? process.env;
  const request = opts.requestImpl ?? defaultRequest(opts.timeoutMs ?? 60_000);

  let currentUrl = url;
  for (let i = 0; i <= maxRedirects; i++) {
    const proxy = resolveProxy(currentUrl, env);
    const { statusCode, headers, body } = await request(currentUrl, {}, proxy);

    if (statusCode >= 200 && statusCode < 300) {
      return body;
    }
    if (statusCode >= 300 && statusCode < 400) {
      const loc = headers.location;
      const next = Array.isArray(loc) ? loc[0] : loc;
      if (!next) {
        throw new DownloadError(`HTTP ${statusCode} from ${currentUrl} with no Location header`);
      }
      currentUrl = new URL(next, currentUrl).toString();
      continue;
    }
    throw new DownloadError(
      `HTTP ${statusCode} from ${currentUrl}` +
        (proxy ? ` (via proxy ${proxy})` : ""),
    );
  }
  throw new DownloadError(`Too many redirects (>${maxRedirects}) starting from ${url}`);
}

function defaultRequest(timeoutMs: number) {
  return (
    targetUrl: string,
    headers: Record<string, string>,
    proxy: string | null,
  ): Promise<{ statusCode: number; headers: Record<string, string | string[] | undefined>; body: Buffer }> =>
    new Promise((resolve, reject) => {
      const target = new URL(targetUrl);
      const isHttps = target.protocol === "https:";
      const mod = isHttps ? https : http;

      const reqOpts: https.RequestOptions = proxy
        ? proxyRequestOptions(target, new URL(proxy), headers)
        : directRequestOptions(target, headers);

      const req = mod.request(reqOpts, (res) => {
        const chunks: Buffer[] = [];
        res.on("data", (c) => chunks.push(c as Buffer));
        res.on("end", () =>
          resolve({
            statusCode: res.statusCode ?? 0,
            headers: res.headers,
            body: Buffer.concat(chunks),
          }),
        );
        res.on("error", reject);
      });
      req.on("error", reject);
      req.setTimeout(timeoutMs, () => {
        req.destroy(new DownloadError(`request timed out after ${timeoutMs}ms: ${targetUrl}`));
      });
      req.end();
    });
}

function directRequestOptions(
  target: URL,
  headers: Record<string, string>,
): https.RequestOptions {
  return {
    method: "GET",
    hostname: target.hostname,
    port: target.port || (target.protocol === "https:" ? 443 : 80),
    path: target.pathname + target.search,
    headers: { "user-agent": "@askenaz-dev/fdh-postinstall", ...headers },
  };
}

function proxyRequestOptions(
  target: URL,
  proxy: URL,
  headers: Record<string, string>,
): https.RequestOptions {
  // Simple HTTP-proxy GET; for HTTPS this is really CONNECT tunneling territory.
  // For the postinstall use case (corporate proxies that accept plain GET with
  // absolute URI), this is sufficient. If a deployment uses strict CONNECT-only
  // proxies, document NODE_EXTRA_CA_CERTS + suggest direct egress instead.
  const auth =
    proxy.username || proxy.password
      ? "Basic " +
        Buffer.from(`${decodeURIComponent(proxy.username)}:${decodeURIComponent(proxy.password)}`).toString("base64")
      : undefined;
  const proxyHeaders: Record<string, string> = {
    host: target.host,
    "user-agent": "@askenaz-dev/fdh-postinstall",
    ...headers,
  };
  if (auth) proxyHeaders["proxy-authorization"] = auth;

  return {
    method: "GET",
    hostname: proxy.hostname,
    port: proxy.port || (proxy.protocol === "https:" ? 443 : 80),
    path: target.toString(), // absolute URI through the proxy
    headers: proxyHeaders,
  };
}

export class DownloadError extends Error {
  constructor(message: string) {
    super(`fdh: ${message}`);
    this.name = "DownloadError";
  }
}

// -----------------------------------------------------------------------------
// SHA-256
// -----------------------------------------------------------------------------

export function sha256(buf: Buffer): string {
  return createHash("sha256").update(buf).digest("hex");
}

export async function sha256File(filePath: string): Promise<string> {
  const buf = await fs.readFile(filePath);
  return sha256(buf);
}

/** Parse a `<hex>  <filename>` line as emitted by `shasum -a 256` / `sha256sum`. */
export function parseSha256Manifest(text: string): string | null {
  const trimmed = text.trim();
  if (!trimmed) return null;
  // Accept either a bare 64-char hex digest or the standard "<hash>  <name>" form.
  const bareHex = /^[a-fA-F0-9]{64}$/;
  if (bareHex.test(trimmed)) return trimmed.toLowerCase();
  const parts = trimmed.split(/\s+/);
  if (parts.length >= 1 && bareHex.test(parts[0]!)) {
    return parts[0]!.toLowerCase();
  }
  return null;
}

// -----------------------------------------------------------------------------
// Extraction
// -----------------------------------------------------------------------------

/**
 * Extract a .tar.gz file into `destDir`. Uses the platform's `tar` binary
 * (built into Windows 10 1809+; standard everywhere else).
 *
 * Returns the absolute path to the binary inside destDir, asserting it exists.
 */
export async function extractTarball(
  tarPath: string,
  destDir: string,
  expectedFilename: string,
): Promise<string> {
  await fs.mkdir(destDir, { recursive: true });
  // goreleaser ships .tar.gz for linux/darwin and .zip for windows; extract
  // each with a tool that reliably handles it on that OS.
  const isZip = tarPath.toLowerCase().endsWith(".zip");
  let result;
  if (isZip) {
    // Windows .zip. Use PowerShell's Expand-Archive rather than `tar`: a bare
    // `tar` on Windows can resolve to git's GNU tar (which cannot read zip),
    // and bsdtar misparses '@scope' absolute paths as remote hosts.
    // -LiteralPath handles paths containing '@' and ':' safely.
    result = spawnSync(
      "powershell",
      [
        "-NoProfile",
        "-NonInteractive",
        "-Command",
        `Expand-Archive -LiteralPath ${psQuote(tarPath)} -DestinationPath ${psQuote(destDir)} -Force`,
      ],
      { stdio: ["ignore", "pipe", "pipe"], encoding: "utf8" },
    );
  } else {
    // .tar.gz on macOS/Linux. Run with cwd=destDir + the bare filename so
    // bsdtar (macOS) doesn't misparse an absolute path containing '@' (the
    // '@scope' node_modules dir) as a remote user@host:path spec.
    result = spawnSync("tar", ["-xzf", path.basename(tarPath)], {
      cwd: destDir,
      stdio: ["ignore", "pipe", "pipe"],
      encoding: "utf8",
    });
  }
  if (result.error) {
    throw new ExtractionError(
      `failed to extract ${path.basename(tarPath)}: ${result.error.message}.`,
    );
  }
  if (result.status !== 0) {
    throw new ExtractionError(
      `extraction of ${path.basename(tarPath)} exited with code ${result.status}: ${(result.stderr ?? "").trim()}`,
    );
  }
  const binPath = path.join(destDir, expectedFilename);
  try {
    await fs.access(binPath);
  } catch {
    throw new ExtractionError(
      `tarball did not contain expected file '${expectedFilename}' ` +
        `(extracted to ${destDir}).`,
    );
  }
  if (process.platform !== "win32") {
    await fs.chmod(binPath, 0o755);
  }
  return binPath;
}

/** Single-quote a string for safe interpolation into a PowerShell command. */
function psQuote(s: string): string {
  return `'${s.replace(/'/g, "''")}'`;
}

export class ExtractionError extends Error {
  constructor(message: string) {
    super(`fdh: ${message}`);
    this.name = "ExtractionError";
  }
}

// -----------------------------------------------------------------------------
// Package manager detection
// -----------------------------------------------------------------------------

export type PackageManager = "npm" | "pnpm" | "yarn" | "bun" | "unknown";

/** Best-effort detection of which PM invoked us, via npm_config_user_agent. */
export function detectPackageManager(
  env: NodeJS.ProcessEnv = process.env,
): PackageManager {
  const ua = env.npm_config_user_agent ?? "";
  if (ua.startsWith("pnpm/")) return "pnpm";
  if (ua.startsWith("yarn/")) return "yarn";
  if (ua.startsWith("bun/")) return "bun";
  if (ua.startsWith("npm/")) return "npm";
  return "unknown";
}

// -----------------------------------------------------------------------------
// Paths
// -----------------------------------------------------------------------------

/**
 * Resolve the absolute path to the bin/ directory inside the installed package,
 * relative to a known file inside the package (typically import.meta.url).
 */
export function resolveBinDir(packageRoot: string): string {
  return path.join(packageRoot, "bin");
}

export function packageRootFromDist(distFileUrl: string): string {
  // distFileUrl is `file:///.../@askenaz-dev/fdh/dist/<something>.js`.
  const filePath = new URL(distFileUrl).pathname;
  const normalized =
    process.platform === "win32" && filePath.startsWith("/")
      ? filePath.slice(1)
      : filePath;
  // dist/<x>.js → ..
  return path.resolve(path.dirname(normalized), "..");
}
