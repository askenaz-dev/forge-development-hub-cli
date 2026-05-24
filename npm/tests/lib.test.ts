import { describe, it, expect } from "vitest";

import {
  binaryFilename,
  detectPackageManager,
  download,
  DownloadError,
  parseSha256Manifest,
  resolveProxy,
  resolveTarget,
  sha256,
  SUPPORTED_TARGETS,
  TargetUnsupportedError,
} from "../src/lib.js";

describe("resolveTarget", () => {
  it("maps known platform/arch combos to supported targets", () => {
    expect(resolveTarget("darwin", "arm64")).toBe("darwin-arm64");
    expect(resolveTarget("darwin", "x64")).toBe("darwin-amd64");
    expect(resolveTarget("linux", "arm64")).toBe("linux-arm64");
    expect(resolveTarget("linux", "x64")).toBe("linux-amd64");
    expect(resolveTarget("win32", "x64")).toBe("windows-amd64");
  });

  it("throws TargetUnsupportedError on unknown platform", () => {
    expect(() => resolveTarget("freebsd" as NodeJS.Platform, "x64")).toThrowError(TargetUnsupportedError);
  });

  it("throws TargetUnsupportedError on unknown arch", () => {
    expect(() => resolveTarget("linux", "mips")).toThrowError(TargetUnsupportedError);
  });

  it("error message lists all supported targets", () => {
    try {
      resolveTarget("freebsd" as NodeJS.Platform, "x64");
    } catch (err) {
      expect((err as Error).message).toContain("Supported:");
      for (const t of SUPPORTED_TARGETS) {
        expect((err as Error).message).toContain(t);
      }
    }
  });

  it("rejects windows-arm64 (not in supported matrix)", () => {
    expect(() => resolveTarget("win32", "arm64")).toThrowError(TargetUnsupportedError);
  });
});

describe("binaryFilename", () => {
  it("returns fdh.exe on windows targets", () => {
    expect(binaryFilename("windows-amd64")).toBe("fdh.exe");
  });
  it("returns fdh on non-windows targets", () => {
    expect(binaryFilename("darwin-arm64")).toBe("fdh");
    expect(binaryFilename("linux-amd64")).toBe("fdh");
  });
});

describe("resolveProxy", () => {
  it("returns null when no proxy is configured", () => {
    expect(resolveProxy("https://example.com", {})).toBeNull();
  });

  it("prefers npm_config_https_proxy over HTTPS_PROXY", () => {
    const env = {
      npm_config_https_proxy: "http://npm-proxy:8080",
      HTTPS_PROXY: "http://env-proxy:8080",
    };
    expect(resolveProxy("https://example.com", env)).toBe("http://npm-proxy:8080");
  });

  it("falls back to HTTPS_PROXY when npm_config_https_proxy is unset", () => {
    expect(resolveProxy("https://example.com", { HTTPS_PROXY: "http://env-proxy:8080" })).toBe(
      "http://env-proxy:8080",
    );
  });

  it("uses HTTP_PROXY for http:// URLs", () => {
    expect(resolveProxy("http://example.com", { HTTP_PROXY: "http://p:80" })).toBe("http://p:80");
  });

  it("honors NO_PROXY * wildcard", () => {
    expect(
      resolveProxy("https://example.com", {
        NO_PROXY: "*",
        HTTPS_PROXY: "http://p:8080",
      }),
    ).toBeNull();
  });

  it("honors NO_PROXY exact hostname match", () => {
    expect(
      resolveProxy("https://artifactory.forge.internal/x", {
        NO_PROXY: "artifactory.forge.internal",
        HTTPS_PROXY: "http://p:8080",
      }),
    ).toBeNull();
  });

  it("honors NO_PROXY suffix match (.example.com matches sub.example.com)", () => {
    expect(
      resolveProxy("https://sub.example.com/path", {
        NO_PROXY: ".example.com",
        HTTPS_PROXY: "http://p:8080",
      }),
    ).toBeNull();
  });

  it("honors NO_PROXY suffix match without leading dot", () => {
    expect(
      resolveProxy("https://sub.example.com/path", {
        NO_PROXY: "example.com",
        HTTPS_PROXY: "http://p:8080",
      }),
    ).toBeNull();
  });

  it("does NOT match unrelated hosts in NO_PROXY", () => {
    expect(
      resolveProxy("https://other.com", {
        NO_PROXY: "example.com",
        HTTPS_PROXY: "http://p:8080",
      }),
    ).toBe("http://p:8080");
  });
});

describe("sha256 + parseSha256Manifest", () => {
  it("computes SHA-256 of a buffer", () => {
    expect(sha256(Buffer.from("hello"))).toBe(
      "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
    );
  });

  it("parses bare hex digest", () => {
    const hex = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824";
    expect(parseSha256Manifest(hex)).toBe(hex);
  });

  it("parses sha256sum format (hash, two spaces, filename)", () => {
    const text = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824  fdh-darwin-arm64.tar.gz\n";
    expect(parseSha256Manifest(text)).toBe(
      "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
    );
  });

  it("returns null on garbage input", () => {
    expect(parseSha256Manifest("not a hash")).toBeNull();
    expect(parseSha256Manifest("")).toBeNull();
    expect(parseSha256Manifest("xyz123")).toBeNull();
  });

  it("normalizes uppercase hex to lowercase", () => {
    const upper = "2CF24DBA5FB0A30E26E83B2AC5B9E29E1B161E5C1FA7425E73043362938B9824";
    expect(parseSha256Manifest(upper)).toBe(upper.toLowerCase());
  });
});

describe("detectPackageManager", () => {
  it("detects pnpm from user-agent", () => {
    expect(detectPackageManager({ npm_config_user_agent: "pnpm/9.1.0 npm/? node/v20.10.0" })).toBe("pnpm");
  });
  it("detects yarn from user-agent", () => {
    expect(detectPackageManager({ npm_config_user_agent: "yarn/1.22.22 npm/? node/v18.0.0" })).toBe("yarn");
  });
  it("detects npm from user-agent", () => {
    expect(detectPackageManager({ npm_config_user_agent: "npm/10.0.0 node/v20.0.0" })).toBe("npm");
  });
  it("detects bun from user-agent", () => {
    expect(detectPackageManager({ npm_config_user_agent: "bun/1.0.0" })).toBe("bun");
  });
  it("returns 'unknown' when user-agent is missing", () => {
    expect(detectPackageManager({})).toBe("unknown");
  });
});

describe("download", () => {
  it("returns body on 2xx", async () => {
    const buf = await download("https://example.com/file.bin", {
      requestImpl: async () => ({
        statusCode: 200,
        headers: {},
        body: Buffer.from("payload"),
      }),
    });
    expect(buf.toString()).toBe("payload");
  });

  it("follows redirects up to maxRedirects", async () => {
    let calls = 0;
    const buf = await download("https://example.com/a", {
      maxRedirects: 3,
      requestImpl: async (url) => {
        calls++;
        if (url.endsWith("/a")) {
          return { statusCode: 302, headers: { location: "https://example.com/b" }, body: Buffer.alloc(0) };
        }
        if (url.endsWith("/b")) {
          return { statusCode: 301, headers: { location: "/c" }, body: Buffer.alloc(0) };
        }
        return { statusCode: 200, headers: {}, body: Buffer.from("ok") };
      },
    });
    expect(buf.toString()).toBe("ok");
    expect(calls).toBe(3);
  });

  it("throws DownloadError when redirect chain exceeds maxRedirects", async () => {
    await expect(
      download("https://example.com/loop", {
        maxRedirects: 2,
        requestImpl: async () => ({
          statusCode: 302,
          headers: { location: "https://example.com/loop2" },
          body: Buffer.alloc(0),
        }),
      }),
    ).rejects.toThrowError(DownloadError);
  });

  it("throws DownloadError on 4xx/5xx", async () => {
    await expect(
      download("https://example.com/missing", {
        requestImpl: async () => ({ statusCode: 404, headers: {}, body: Buffer.alloc(0) }),
      }),
    ).rejects.toThrowError(/HTTP 404/);
  });

  it("includes proxy info in error message when proxy was used", async () => {
    await expect(
      download("https://example.com/bad", {
        env: { HTTPS_PROXY: "http://p:8080" },
        requestImpl: async () => ({ statusCode: 500, headers: {}, body: Buffer.alloc(0) }),
      }),
    ).rejects.toThrowError(/via proxy http:\/\/p:8080/);
  });

  it("throws on 3xx without a Location header", async () => {
    await expect(
      download("https://example.com/foo", {
        requestImpl: async () => ({ statusCode: 302, headers: {}, body: Buffer.alloc(0) }),
      }),
    ).rejects.toThrowError(/no Location header/);
  });
});
