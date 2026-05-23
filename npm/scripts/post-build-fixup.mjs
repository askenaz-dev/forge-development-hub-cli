// post-build-fixup.mjs
//
// `tsc` produces .js files but doesn't add the `#!/usr/bin/env node` shebang
// nor set the executable bit (Unix). npm needs both to invoke `bin/*` entries
// as scripts. This script runs after `tsc` to fix that up.

import { promises as fs } from "node:fs";
import { join } from "node:path";

const BIN_FILES = ["cli.js", "cli-alias.js", "postinstall.js"];
const SHEBANG = "#!/usr/bin/env node\n";

async function main() {
  const distDir = new URL("../dist/", import.meta.url).pathname;
  // On Windows, URL pathname starts with `/C:/...`. Strip the leading slash.
  const dir = process.platform === "win32" && distDir.startsWith("/")
    ? distDir.slice(1)
    : distDir;

  for (const name of BIN_FILES) {
    const path = join(dir, name);
    let content;
    try {
      content = await fs.readFile(path, "utf8");
    } catch (err) {
      console.error(`post-build-fixup: skip ${name} (${err.code || err.message})`);
      continue;
    }
    if (!content.startsWith("#!")) {
      content = SHEBANG + content;
      await fs.writeFile(path, content, "utf8");
    }
    if (process.platform !== "win32") {
      await fs.chmod(path, 0o755);
    }
    console.log(`post-build-fixup: ${name} ok`);
  }
}

main().catch((err) => {
  console.error("post-build-fixup failed:", err);
  process.exit(1);
});
