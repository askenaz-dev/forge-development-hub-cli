import { headers } from "next/headers";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { CopyCommand } from "@/components/copy-command";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { getTranslations } from "next-intl/server";

/**
 * /install — the "how do I install fdh" page.
 *
 * The page is server-rendered. We sniff the User-Agent on the server to
 * pick the default-active tab so a Windows visitor sees the Windows
 * commands first without any client-side JavaScript.
 *
 * Real release URLs come in once M10 has the package-manager target. For
 * now the URLs point at the placeholder host `pkg.forge.internal`.
 */
const VERSION = process.env.NEXT_PUBLIC_FDH_VERSION ?? "v0.1.0";
const PKG_HOST = process.env.NEXT_PUBLIC_FDH_PKG_HOST ?? "https://pkg.forge.internal/fdh";

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

function specs(): Record<Platform, PlatformSpec> {
  const base = `${PKG_HOST}/${VERSION}`;
  return {
    "macos-arm64": {
      id: "macos-arm64",
      archive: `fdh-${VERSION}-darwin-arm64.tar.gz`,
      download: `curl -fsSL -O ${base}/fdh-${VERSION}-darwin-arm64.tar.gz -O ${base}/fdh-${VERSION}-darwin-arm64.tar.gz.sha256`,
      checksum: `fdh-${VERSION}-darwin-arm64.tar.gz.sha256`,
      verify: `shasum -a 256 -c fdh-${VERSION}-darwin-arm64.tar.gz.sha256`,
      extract: `tar -xzf fdh-${VERSION}-darwin-arm64.tar.gz`,
      place: `sudo mv fdh-${VERSION}-darwin-arm64/fdh /usr/local/bin/`,
    },
    "macos-amd64": {
      id: "macos-amd64",
      archive: `fdh-${VERSION}-darwin-amd64.tar.gz`,
      download: `curl -fsSL -O ${base}/fdh-${VERSION}-darwin-amd64.tar.gz -O ${base}/fdh-${VERSION}-darwin-amd64.tar.gz.sha256`,
      checksum: `fdh-${VERSION}-darwin-amd64.tar.gz.sha256`,
      verify: `shasum -a 256 -c fdh-${VERSION}-darwin-amd64.tar.gz.sha256`,
      extract: `tar -xzf fdh-${VERSION}-darwin-amd64.tar.gz`,
      place: `sudo mv fdh-${VERSION}-darwin-amd64/fdh /usr/local/bin/`,
    },
    "linux-arm64": {
      id: "linux-arm64",
      archive: `fdh-${VERSION}-linux-arm64.tar.gz`,
      download: `curl -fsSL -O ${base}/fdh-${VERSION}-linux-arm64.tar.gz -O ${base}/fdh-${VERSION}-linux-arm64.tar.gz.sha256`,
      checksum: `fdh-${VERSION}-linux-arm64.tar.gz.sha256`,
      verify: `sha256sum -c fdh-${VERSION}-linux-arm64.tar.gz.sha256`,
      extract: `tar -xzf fdh-${VERSION}-linux-arm64.tar.gz`,
      place: `sudo mv fdh-${VERSION}-linux-arm64/fdh /usr/local/bin/`,
    },
    "linux-amd64": {
      id: "linux-amd64",
      archive: `fdh-${VERSION}-linux-amd64.tar.gz`,
      download: `curl -fsSL -O ${base}/fdh-${VERSION}-linux-amd64.tar.gz -O ${base}/fdh-${VERSION}-linux-amd64.tar.gz.sha256`,
      checksum: `fdh-${VERSION}-linux-amd64.tar.gz.sha256`,
      verify: `sha256sum -c fdh-${VERSION}-linux-amd64.tar.gz.sha256`,
      extract: `tar -xzf fdh-${VERSION}-linux-amd64.tar.gz`,
      place: `sudo mv fdh-${VERSION}-linux-amd64/fdh /usr/local/bin/`,
    },
    "windows-amd64": {
      id: "windows-amd64",
      archive: `fdh-${VERSION}-windows-amd64.tar.gz`,
      download: `Invoke-WebRequest ${base}/fdh-${VERSION}-windows-amd64.tar.gz -OutFile fdh.tar.gz; Invoke-WebRequest ${base}/fdh-${VERSION}-windows-amd64.tar.gz.sha256 -OutFile fdh.tar.gz.sha256`,
      checksum: `fdh-${VERSION}-windows-amd64.tar.gz.sha256`,
      verify: `Get-FileHash fdh.tar.gz -Algorithm SHA256 # compare to fdh.tar.gz.sha256`,
      extract: `tar -xzf fdh.tar.gz`,
      place: `# Move fdh.exe into a directory on PATH, e.g. C:\\Tools\\\n# Then verify with: fdh --version`,
    },
  };
}

function detectPlatform(userAgent: string | undefined): Platform {
  const ua = (userAgent ?? "").toLowerCase();
  if (ua.includes("win")) return "windows-amd64";
  if (ua.includes("mac")) {
    // Apple Silicon is everywhere now; default to arm64.
    return "macos-arm64";
  }
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
  const all = specs();

  const tabs: { id: Platform; label: string; group: string }[] = [
    { id: "macos-arm64", label: "macOS (Apple Silicon)", group: "mac" },
    { id: "macos-amd64", label: "macOS (Intel)", group: "mac" },
    { id: "linux-amd64", label: "Linux (amd64)", group: "linux" },
    { id: "linux-arm64", label: "Linux (arm64)", group: "linux" },
    { id: "windows-amd64", label: "Windows (amd64)", group: "windows" },
  ];

  return (
    <div className="container py-12">
      <header className="mx-auto max-w-3xl text-center">
        <h1 className="text-3xl font-bold tracking-tight">{t("title")}</h1>
        <p className="mt-3 text-muted-foreground">{t("intro")}</p>
      </header>

      <div className="mx-auto mt-10 max-w-4xl">
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
  );
}
