// postinstall.ts — runs synchronously during `npm install` / `npx` / `pnpm add`
// / `yarn add`. Detects the host's platform+arch, downloads the matching Go
// binary from $FDH_PKG_HOST, verifies SHA-256, and extracts to bin/ inside
// this package. Cache-hit aware: if the binary already exists with the right
// checksum, it skips the network entirely.
//
// Honors corporate proxies (npm_config_https_proxy → HTTPS_PROXY → direct)
// and NO_PROXY.
//
// Idempotent. Fails fast with actionable messages on any error.

import { promises as fs } from "node:fs";
import * as path from "node:path";
import { fileURLToPath } from "node:url";

import {
  binaryFilename,
  detectPackageManager,
  download,
  DownloadError,
  ExtractionError,
  extractTarball,
  packageRootFromDist,
  parseSha256Manifest,
  resolveBinDir,
  resolveTarget,
  sha256File,
  type SupportedTarget,
  TargetUnsupportedError,
} from "./lib.js";

// -----------------------------------------------------------------------------
// Config from env / package.json
// -----------------------------------------------------------------------------

function readPkgVersion(packageRoot: string): string {
  // Synchronous read at script start; throws if package.json is unreadable.
  const pkgPath = path.join(packageRoot, "package.json");
  // eslint-disable-next-line @typescript-eslint/no-var-requires
  const text = require("node:fs").readFileSync(pkgPath, "utf8");
  const parsed = JSON.parse(text) as { version?: string };
  if (!parsed.version) {
    throw new Error(`postinstall: package.json at ${pkgPath} has no "version" field`);
  }
  return parsed.version;
}

function readPkgHost(env: NodeJS.ProcessEnv = process.env): string {
  return (
    env.FDH_PKG_HOST ??
    // Default placeholder until the platform team confirms the real host.
    "pkg.forge.internal"
  );
}

interface DownloadPaths {
  tarballUrl: string;
  shaUrl: string;
  tarballName: string;
}

/**
 * Build the URLs to download the binary tarball + its SHA-256 sidecar.
 *
 * Matches the convention used by the existing goreleaser pipeline and the
 * `publish` job in `.github/workflows/release.yml`:
 *
 *   https://<host>/fdh/v<version>/fdh_v<version>_<os>_<arch>.tar.gz
 *   https://<host>/fdh/v<version>/fdh_v<version>_<os>_<arch>.tar.gz.sha256
 *
 * Targets like `darwin-arm64` map to filename segment `darwin_arm64`.
 */
function buildUrls(host: string, version: string, target: SupportedTarget): DownloadPaths {
  const cleanHost = host.replace(/^https?:\/\//, "").replace(/\/$/, "");
  const cleanVer = version.replace(/^v/, ""); // canonical: 0.7.2
  const tag = `v${cleanVer}`; // canonical: v0.7.2
  const filenameTarget = target.replace("-", "_"); // darwin-arm64 → darwin_arm64
  const tarballName = `fdh_${tag}_${filenameTarget}.tar.gz`;
  const base = `https://${cleanHost}/fdh/${tag}`;
  return {
    tarballUrl: `${base}/${tarballName}`,
    shaUrl: `${base}/${tarballName}.sha256`,
    tarballName,
  };
}

// -----------------------------------------------------------------------------
// Main
// -----------------------------------------------------------------------------

async function main(): Promise<void> {
  // Opt-out for CI / dev workflows that don't want to fetch a binary.
  if (process.env.FDH_SKIP_POSTINSTALL === "1" || process.env.FDH_SKIP_POSTINSTALL === "true") {
    console.log("fdh postinstall: skipped via FDH_SKIP_POSTINSTALL");
    return;
  }

  const packageRoot = packageRootFromDist(import.meta.url);
  const version = readPkgVersion(packageRoot);
  const pkgHost = readPkgHost();

  let target: SupportedTarget;
  try {
    target = resolveTarget();
  } catch (err) {
    if (err instanceof TargetUnsupportedError) {
      console.error(err.message);
      process.exit(78);
    }
    throw err;
  }

  const binDir = resolveBinDir(packageRoot);
  const binPath = path.join(binDir, binaryFilename(target));

  const { tarballUrl, shaUrl, tarballName } = buildUrls(pkgHost, version, target);
  const pm = detectPackageManager();

  const banner = `fdh postinstall (${pm}): target=${target} version=v${version.replace(/^v/, "")}`;
  console.log(banner);

  // ---------------------------------------------------------------------------
  // Cache hit check
  // ---------------------------------------------------------------------------
  if (await pathExists(binPath)) {
    // We have a binary already; check it matches what we'd download.
    let expectedSha: string | null = null;
    try {
      const shaBuf = await download(shaUrl);
      expectedSha = parseSha256Manifest(shaBuf.toString("utf8"));
    } catch (err) {
      // Can't reach the registry to check — if we already have a binary, that
      // matches our installed version (we don't bump version between rebuilds),
      // assume cache is valid. Print a soft warning.
      console.warn(
        `fdh postinstall: cache hit by presence (could not verify SHA against ` +
          `${shaUrl}: ${(err as Error).message}). Skipping download.`,
      );
      return;
    }
    if (expectedSha) {
      const actual = await sha256File(binPath);
      if (actual === expectedSha) {
        console.log(`fdh postinstall: cache hit (sha256 verified) at ${binPath}`);
        return;
      }
      console.log(
        `fdh postinstall: cache miss (sha256 differs; expected ${expectedSha.slice(0, 8)}…, got ${actual.slice(0, 8)}…). Re-downloading.`,
      );
    }
  }

  // ---------------------------------------------------------------------------
  // Download tarball + sha
  // ---------------------------------------------------------------------------
  let tarballBuf: Buffer;
  let shaText: string;
  try {
    tarballBuf = await download(tarballUrl);
    const shaBuf = await download(shaUrl);
    shaText = shaBuf.toString("utf8");
  } catch (err) {
    if (err instanceof DownloadError) {
      console.error(err.message);
      console.error(
        `\nTroubleshooting:\n` +
          `  • Verify FDH_PKG_HOST is set (current: ${pkgHost}).\n` +
          `  • Check your proxy: HTTP(S)_PROXY=${process.env.HTTPS_PROXY ?? process.env.HTTP_PROXY ?? "<unset>"}, NO_PROXY=${process.env.NO_PROXY ?? "<unset>"}.\n` +
          `  • Behind a corporate cert-inspection proxy? Set NODE_EXTRA_CA_CERTS to your CA bundle.\n` +
          `  • See docs/troubleshooting.md.`,
      );
      process.exit(1);
    }
    throw err;
  }

  // ---------------------------------------------------------------------------
  // Verify SHA-256
  // ---------------------------------------------------------------------------
  const expectedSha = parseSha256Manifest(shaText);
  if (!expectedSha) {
    console.error(
      `fdh postinstall: SHA-256 manifest at ${shaUrl} could not be parsed.\n` +
        `Got:\n${truncate(shaText, 200)}`,
    );
    process.exit(1);
  }
  const { createHash } = await import("node:crypto");
  const actualSha = createHash("sha256").update(tarballBuf).digest("hex");
  if (actualSha !== expectedSha) {
    console.error(
      `fdh postinstall: integrity check failed for ${tarballName}.\n` +
        `  expected ${expectedSha}\n  got      ${actualSha}\n` +
        `Aborting install. The binary will NOT be placed on PATH.\n` +
        `If you trust the source, report this to dx-platform with the URL: ${tarballUrl}`,
    );
    process.exit(1);
  }

  // ---------------------------------------------------------------------------
  // Write tarball to disk + extract
  // ---------------------------------------------------------------------------
  await fs.mkdir(binDir, { recursive: true });
  const tarballPath = path.join(binDir, tarballName);
  await fs.writeFile(tarballPath, tarballBuf);

  try {
    const finalBin = await extractTarball(tarballPath, binDir, binaryFilename(target));
    console.log(`fdh postinstall: installed ${path.relative(packageRoot, finalBin)} (${tarballBuf.length} bytes)`);
  } catch (err) {
    if (err instanceof ExtractionError) {
      console.error(err.message);
      process.exit(1);
    }
    throw err;
  } finally {
    // Drop the tarball to save disk; cache hit is keyed off the binary's SHA.
    await fs.rm(tarballPath, { force: true });
  }
}

async function pathExists(p: string): Promise<boolean> {
  try {
    await fs.access(p);
    return true;
  } catch {
    return false;
  }
}

function truncate(s: string, max: number): string {
  return s.length <= max ? s : s.slice(0, max) + "…";
}

// Tests import this file but should NOT execute main().
const isThisFile = fileURLToPath(import.meta.url) === process.argv[1] ||
  process.argv[1]?.endsWith("postinstall.js");

if (isThisFile) {
  main().catch((err) => {
    console.error("fdh postinstall: unexpected error:", err);
    process.exit(1);
  });
}

export { main as runPostinstall, buildUrls, readPkgVersion };
