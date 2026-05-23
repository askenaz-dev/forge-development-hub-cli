import { describe, it, expect } from "vitest";

import { packageRootFromDist, resolveBinDir } from "../src/lib.js";

// The wrapper itself (src/cli.ts) spawns a child process and inherits stdio,
// which is hard to assert on without launching real binaries. Coverage at the
// unit level focuses on the helpers that the wrapper depends on; the e2e
// behavior (spawn + exit code propagation) is covered by the CI matrix that
// installs the real published package and runs `fdh --version`.

describe("packageRootFromDist", () => {
  it("strips dist/<file>.js segment (POSIX)", () => {
    if (process.platform === "win32") return; // path.resolve on Windows resolves POSIX strings against cwd
    const fakeDistUrl = "file:///home/dev/proj/dist/cli.js";
    expect(packageRootFromDist(fakeDistUrl)).toBe("/home/dev/proj");
  });

  it("strips dist/<file>.js segment (Windows)", () => {
    if (process.platform !== "win32") return; // file URLs are platform-specific
    const fakeDistUrl = "file:///C:/proj/dist/cli.js";
    const root = packageRootFromDist(fakeDistUrl);
    expect(root.replace(/\\/g, "/")).toBe("C:/proj");
  });
});

describe("resolveBinDir", () => {
  it("returns <packageRoot>/bin", () => {
    expect(resolveBinDir("/abs/pkg")).toMatch(/[\\/]bin$/);
  });
});
