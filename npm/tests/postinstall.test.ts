import { describe, it, expect } from "vitest";

import { buildUrls } from "../src/postinstall.js";

describe("buildUrls", () => {
  it("composes GitHub Releases tarball + sha URLs from base + version + target", () => {
    const u = buildUrls(
      "https://github.com/askenaz-dev/forge-development-hub-cli/releases",
      "0.7.2",
      "darwin-arm64",
    );
    expect(u.tarballUrl).toBe(
      "https://github.com/askenaz-dev/forge-development-hub-cli/releases/download/v0.7.2/fdh_v0.7.2_darwin_arm64.tar.gz",
    );
    expect(u.shaUrl).toBe(
      "https://github.com/askenaz-dev/forge-development-hub-cli/releases/download/v0.7.2/fdh_v0.7.2_darwin_arm64.tar.gz.sha256",
    );
    expect(u.tarballName).toBe("fdh_v0.7.2_darwin_arm64.tar.gz");
  });

  it("adds https:// prefix when bare host is passed (private mirror shorthand)", () => {
    const u = buildUrls("pkg.askenaz.dev/fdh", "1.0.0", "linux-amd64");
    expect(u.tarballUrl).toBe(
      "https://pkg.askenaz.dev/fdh/download/v1.0.0/fdh_v1.0.0_linux_amd64.tar.gz",
    );
  });

  it("strips trailing slash from base if present", () => {
    const u = buildUrls("https://example.com/releases/", "1.0.0", "linux-amd64");
    expect(u.tarballUrl).not.toContain("releases//download");
    expect(u.tarballUrl).toBe(
      "https://example.com/releases/download/v1.0.0/fdh_v1.0.0_linux_amd64.tar.gz",
    );
  });

  it("preserves an explicit http:// prefix (for local test mirrors)", () => {
    const u = buildUrls("http://localhost:9000/releases", "1.0.0", "linux-amd64");
    expect(u.tarballUrl).toBe(
      "http://localhost:9000/releases/download/v1.0.0/fdh_v1.0.0_linux_amd64.tar.gz",
    );
  });

  it("normalizes version with or without leading v", () => {
    const a = buildUrls("https://example.com/releases", "v1.2.3", "linux-amd64");
    const b = buildUrls("https://example.com/releases", "1.2.3", "linux-amd64");
    expect(a.tarballUrl).toBe(b.tarballUrl);
    expect(a.tarballName).toBe("fdh_v1.2.3_linux_amd64.tar.gz");
  });

  it("converts hyphenated target to underscored filename segment", () => {
    expect(
      buildUrls("https://example.com/releases", "1.0.0", "darwin-arm64").tarballName,
    ).toBe("fdh_v1.0.0_darwin_arm64.tar.gz");
    expect(
      buildUrls("https://example.com/releases", "1.0.0", "linux-arm64").tarballName,
    ).toBe("fdh_v1.0.0_linux_arm64.tar.gz");
    expect(
      buildUrls("https://example.com/releases", "1.0.0", "windows-amd64").tarballName,
    ).toBe("fdh_v1.0.0_windows_amd64.tar.gz");
  });
});
