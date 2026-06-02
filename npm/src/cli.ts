// Entry point for the `fdh` bin. Wraps the platform binary installed by
// postinstall.ts and forwards argv + propagates exit code.
//
// Shipped to consumers as dist/cli.js with a shebang added by scripts/post-build-fixup.mjs.

import { spawn, spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import * as path from "node:path";

import {
  binaryFilename,
  packageRootFromDist,
  resolveBinDir,
  resolveTarget,
  TargetUnsupportedError,
} from "./lib.js";

function main(): never {
  let target;
  try {
    target = resolveTarget();
  } catch (err) {
    if (err instanceof TargetUnsupportedError) {
      console.error(err.message);
      process.exit(78); // EX_CONFIG
    }
    throw err;
  }

  const packageRoot = packageRootFromDist(import.meta.url);
  const binDir = resolveBinDir(packageRoot);
  const binPath = path.join(binDir, binaryFilename(target));

  if (!existsSync(binPath)) {
    // Self-heal: the binary isn't here, which means postinstall never ran —
    // typically because lifecycle scripts were blocked (`npm install
    // --ignore-scripts`, or pnpm/yarn declining build scripts by default).
    // Rather than send the user off to run more commands, fetch the binary
    // now by invoking the bundled postinstall. This makes `npm install` a
    // complete installation method: a wrapper that always ends up with a
    // working binary, not a shortcut that needs a manual repair step.
    const postinstallJs = path.join(packageRoot, "dist", "postinstall.js");
    if (existsSync(postinstallJs)) {
      console.error("fdh: fetching the platform binary (one-time; postinstall was skipped)…");
      spawnSync(process.execPath, [postinstallJs], {
        stdio: "inherit",
        windowsHide: true,
      });
    }
  }

  if (!existsSync(binPath)) {
    console.error(
      `fdh: binary not found at ${binPath}\n` +
        `\n` +
        `Automatic fetch failed (offline, blocked egress, or a proxy/cert issue).\n` +
        `Repair by running:\n` +
        `  npm rebuild @askenaz-dev/fdh\n` +
        `(replace 'npm' with pnpm/yarn/bun if you installed with those),\n` +
        `or set FDH_RELEASES_BASE to a reachable mirror and retry.`,
    );
    process.exit(127); // command not found
  }

  // Forward all argv (skip node + script path) with inherited stdio.
  // shell:false because we control the binary path; no injection risk.
  const child = spawn(binPath, process.argv.slice(2), {
    stdio: "inherit",
    windowsHide: true,
  });
  child.on("error", (err) => {
    console.error(`fdh: failed to launch ${binPath}: ${err.message}`);
    process.exit(126); // command invoked cannot execute
  });
  child.on("exit", (code, signal) => {
    if (signal) {
      // Re-raise the signal on the parent process so wrappers see the same
      // termination reason.
      process.kill(process.pid, signal);
      return;
    }
    process.exit(code ?? 0);
  });
  // Keep the event loop alive until the child exits.
  return undefined as never;
}

// Only run main() when this file is the direct script entry point — NOT
// when imported by cli-alias.ts (which is its own bin) or by tests.
//
// The check matches against `cli.js` specifically; importers that resolve to
// `cli-alias.js` or `*.test.ts` are intentionally excluded.
function isThisFileScriptEntry(): boolean {
  const argv1 = process.argv[1];
  if (!argv1) return false;
  // Match the exact filename `cli.js` at the end of argv[1], allowing for
  // any preceding path separator (`/` or `\`).
  return /[\\/]cli\.js$/.test(argv1);
}

if (isThisFileScriptEntry()) {
  main();
}

// Export for testing / for cli-alias to delegate.
export { main as runFdh };
