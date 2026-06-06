import { headers } from "next/headers";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { CopyCommand } from "@/components/copy-command";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { getTranslations } from "next-intl/server";

/**
 * /install — the "how do I install fdh" page.
 *
 * Server-rendered; we sniff the User-Agent to pick the default-active tab.
 *
 * Canonical artifact contract (single source of truth = goreleaser):
 *   <base>/<tag>/fdh_<ver>_<os>_<arch>.tar.gz       (linux, darwin)
 *   <base>/<tag>/fdh_<ver>_windows_amd64.zip         (windows)
 *   <asset>.sha256                                   (split per-artifact)
 * where <tag> = "v0.2.0" (keeps the "v") and <ver> = "0.2.0" (no "v"),
 * segments separated by underscores. The binary sits at the archive root.
 *
 * The version is resolved from the latest GitHub Release at request time
 * (cached 1h) so this page never goes stale on a new release. Override the
 * download host with NEXT_PUBLIC_FDH_RELEASES_BASE for a private mirror, or
 * pin the version with NEXT_PUBLIC_FDH_VERSION.
 */
const REPO = "askenaz-dev/forge-development-hub-cli";
const PKG_HOST =
  process.env.NEXT_PUBLIC_FDH_RELEASES_BASE ??
  `https://github.com/${REPO}/releases/download`;
const NPM_PKG = "@askenaz-dev/fdh";
const FALLBACK_VERSION = "v0.2.0";

async function resolveVersionTag(): Promise<string> {
  const override = process.env.NEXT_PUBLIC_FDH_VERSION;
  if (override) return override.startsWith("v") ? override : `v${override}`;
  try {
    const res = await fetch(
      `https://api.github.com/repos/${REPO}/releases/latest`,
      {
        next: { revalidate: 3600 },
        headers: {
          accept: "application/vnd.github+json",
          // GitHub's API serves `zstd`/`br`-compressed responses, which Node
          // 22's fetch/undici can't decompress — it throws
          // "controller[kState].transformAlgorithm is not a function" while
          // Next.js reads the body for its ISR cache, polluting the logs and
          // forcing the FALLBACK_VERSION. Ask for an uncompressed response.
          "accept-encoding": "identity",
        },
      }
    );
    if (res.ok) {
      const data = (await res.json()) as { tag_name?: string };
      if (data.tag_name) return data.tag_name;
    }
  } catch {
    // network/ratelimit — fall through to the pinned fallback
  }
  return FALLBACK_VERSION;
}

type Platform = "macos-arm64" | "macos-amd64" | "linux-arm64" | "linux-amd64" | "windows-amd64";

interface PlatformSpec {
  id: Platform;
  archive: string;
  download: string;
  checksum: string;
  verify: string;
  extract: string;
  place: string;
}

function specs(tag: string): Record<Platform, PlatformSpec> {
  const ver = tag.replace(/^v/, ""); // 0.2.0
  const base = `${PKG_HOST}/${tag}`; // .../download/v0.2.0

  const unix = (os: string, arch: string, sha: string): Omit<PlatformSpec, "id"> => {
    const archive = `fdh_${ver}_${os}_${arch}.tar.gz`;
    return {
      archive,
      download: `curl -fsSL -O ${base}/${archive} -O ${base}/${archive}.sha256`,
      checksum: `${archive}.sha256`,
      verify: `${sha} -c ${archive}.sha256`,
      extract: `tar -xzf ${archive}`,
      place: `sudo mv fdh /usr/local/bin/`,
    };
  };

  const winArchive = `fdh_${ver}_windows_amd64.zip`;
  return {
    "macos-arm64": { id: "macos-arm64", ...unix("darwin", "arm64", "shasum -a 256") },
    "macos-amd64": { id: "macos-amd64", ...unix("darwin", "amd64", "shasum -a 256") },
    "linux-arm64": { id: "linux-arm64", ...unix("linux", "arm64", "sha256sum") },
    "linux-amd64": { id: "linux-amd64", ...unix("linux", "amd64", "sha256sum") },
    "windows-amd64": {
      id: "windows-amd64",
      archive: winArchive,
      download: `Invoke-WebRequest ${base}/${winArchive} -OutFile fdh.zip; Invoke-WebRequest ${base}/${winArchive}.sha256 -OutFile fdh.zip.sha256`,
      checksum: `${winArchive}.sha256`,
      verify: `Get-FileHash fdh.zip -Algorithm SHA256  # compare to the hash in fdh.zip.sha256`,
      extract: `Expand-Archive fdh.zip -DestinationPath . -Force`,
      place: `# Move fdh.exe into a directory on PATH, e.g. C:\\Tools\\\n# Then verify with: fdh --version`,
    },
  };
}

function detectPlatform(userAgent: string | undefined): Platform {
  const ua = (userAgent ?? "").toLowerCase();
  if (ua.includes("win")) return "windows-amd64";
  if (ua.includes("mac")) return "macos-arm64";
  if (ua.includes("linux")) {
    if (ua.includes("aarch64") || ua.includes("arm64")) return "linux-arm64";
    return "linux-amd64";
  }
  return "linux-amd64";
}

export default async function InstallPage() {
  const t = await getTranslations("install");
  const h = await headers();
  const userAgent = h.get("user-agent") ?? "";
  const platform = detectPlatform(userAgent);
  const tag = await resolveVersionTag();
  const all = specs(tag);

  const tabs: { id: Platform; label: string }[] = [
    { id: "macos-arm64", label: "macOS (Apple Silicon)" },
    { id: "macos-amd64", label: "macOS (Intel)" },
    { id: "linux-amd64", label: "Linux (amd64)" },
    { id: "linux-arm64", label: "Linux (arm64)" },
    { id: "windows-amd64", label: "Windows (amd64)" },
  ];

  return (
    <div className="container py-12">
      <header className="mx-auto max-w-3xl text-center">
        <h1 className="text-3xl font-bold tracking-tight">{t("title")}</h1>
        <p className="mt-3 text-muted-foreground">{t("intro")}</p>
      </header>

      {/* Primary, cross-platform channel: npm */}
      <Card className="mx-auto mt-10 max-w-2xl border-primary/40">
        <CardHeader>
          <CardTitle>Recommended — install with npm</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="text-sm text-muted-foreground">
            Requires Node.js 18+. Works on macOS, Linux, and Windows. The
            matching binary is fetched and checksum-verified automatically.
          </p>
          <CopyCommand command={`npm i -g ${NPM_PKG}`} />
          <p className="text-sm text-muted-foreground">Or run without installing:</p>
          <CopyCommand command={`npx ${NPM_PKG} init`} />
        </CardContent>
      </Card>

      <div className="mx-auto mt-12 max-w-4xl">
        <h2 className="text-xl font-semibold tracking-tight">
          Or download the binary{" "}
          <span className="font-mono text-sm font-normal text-muted-foreground">
            ({tag})
          </span>
        </h2>
        <div className="mt-4">
          <Tabs defaultValue={platform} className="w-full">
            <TabsList className="grid w-full grid-cols-2 sm:grid-cols-5">
              {tabs.map((tab) => (
                <TabsTrigger key={tab.id} value={tab.id}>
                  {tab.label}
                </TabsTrigger>
              ))}
            </TabsList>
            {tabs.map((tab) => {
              const spec = all[tab.id];
              return (
                <TabsContent key={tab.id} value={tab.id} className="space-y-4">
                  <Card>
                    <CardHeader>
                      <CardTitle>1. Download archive + checksum</CardTitle>
                    </CardHeader>
                    <CardContent>
                      <CopyCommand command={spec.download} />
                    </CardContent>
                  </Card>
                  <Card>
                    <CardHeader>
                      <CardTitle>2. {t("verifyHeading")}</CardTitle>
                    </CardHeader>
                    <CardContent className="space-y-2">
                      <p className="text-sm text-muted-foreground">{t("verifyHint")}</p>
                      <CopyCommand command={spec.verify} />
                    </CardContent>
                  </Card>
                  <Card>
                    <CardHeader>
                      <CardTitle>3. Extract</CardTitle>
                    </CardHeader>
                    <CardContent>
                      <CopyCommand command={spec.extract} />
                    </CardContent>
                  </Card>
                  <Card>
                    <CardHeader>
                      <CardTitle>4. Place on PATH</CardTitle>
                    </CardHeader>
                    <CardContent>
                      <CopyCommand command={spec.place} />
                    </CardContent>
                  </Card>
                  <Card>
                    <CardHeader>
                      <CardTitle>5. Verify</CardTitle>
                    </CardHeader>
                    <CardContent>
                      <CopyCommand command={`fdh --version`} />
                    </CardContent>
                  </Card>
                </TabsContent>
              );
            })}
          </Tabs>
        </div>
      </div>
    </div>
  );
}
