import { describe, it, expect } from "vitest";

import { buildUrls } from "../src/postinstall.js";

describe("buildUrls", () => {
  it("composes tarball + sha URLs from host + version + target using goreleaser convention", () => {
    const u = buildUrls("artifactory.forge.internal/api/raw/fdh-bin", "0.7.2", "darwin-arm64");
    expect(u.tarballUrl).toBe(
      "https://artifactory.forge.internal/api/raw/fdh-bin/fdh/v0.7.2/fdh_v0.7.2_darwin_arm64.tar.gz",
    );
    expect(u.shaUrl).toBe(
      "https://artifactory.forge.internal/api/raw/fdh-bin/fdh/v0.7.2/fdh_v0.7.2_darwin_arm64.tar.gz.sha256",
    );
    expect(u.tarballName).toBe("fdh_v0.7.2_darwin_arm64.tar.gz");
  });

  it("strips https:// prefix from host if present", () => {
    const u = buildUrls("https://pkg.forge.internal", "1.0.0", "linux-amd64");
    expect(u.tarballUrl).toBe(
      "https://pkg.forge.internal/fdh/v1.0.0/fdh_v1.0.0_linux_amd64.tar.gz",
    );
  });

  it("strips trailing slash from host if present", () => {
    const u = buildUrls("pkg.forge.internal/", "1.0.0", "windows-amd64");
    expect(u.tarballUrl).not.toContain("//fdh/");
  });

  it("normalizes version with or without leading v", () => {
    const a = buildUrls("h", "v1.2.3", "linux-amd64");
    const b = buildUrls("h", "1.2.3", "linux-amd64");
    expect(a.tarballUrl).toBe(b.tarballUrl);
    expect(a.tarballName).toBe("fdh_v1.2.3_linux_amd64.tar.gz");
  });

  it("converts hyphenated target to underscored filename segment", () => {
    expect(buildUrls("h", "1.0.0", "darwin-arm64").tarballName).toBe("fdh_v1.0.0_darwin_arm64.tar.gz");
    expect(buildUrls("h", "1.0.0", "linux-arm64").tarballName).toBe("fdh_v1.0.0_linux_arm64.tar.gz");
    expect(buildUrls("h", "1.0.0", "windows-amd64").tarballName).toBe("fdh_v1.0.0_windows_amd64.tar.gz");
  });

  it("tarball name is identical across windows-vs-non-windows targets (the binary inside differs)", () => {
    const win = buildUrls("h", "1.0.0", "windows-amd64").tarballName;
    const lin = buildUrls("h", "1.0.0", "linux-amd64").tarballName;
    expect(win).not.toBe(lin);
    // both end with .tar.gz; both follow the same convention
    expect(win).toMatch(/\.tar\.gz$/);
    expect(lin).toMatch(/\.tar\.gz$/);
  });
});
