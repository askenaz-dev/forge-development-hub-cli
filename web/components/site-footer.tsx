import Link from "next/link";

/**
 * SiteFooter is the global footer with links to docs, accessibility,
 * and a build-info label so users can report against a specific deploy.
 */
export function SiteFooter() {
  const buildVersion = process.env.NEXT_PUBLIC_FDH_BUILD ?? "dev";
  return (
    <footer className="border-t bg-background">
      <div className="container flex flex-col items-center justify-between gap-3 py-6 text-xs text-muted-foreground sm:flex-row">
        <p>
          Forge Development Hub · build{" "}
          <code className="font-mono">{buildVersion}</code>
        </p>
        <nav className="flex items-center gap-3">
          <Link href="/docs" className="hover:underline">
            Docs
          </Link>
          <Link href="/accessibility" className="hover:underline">
            Accessibility
          </Link>
          <Link
            href="https://agentskills.io"
            target="_blank"
            rel="noreferrer noopener"
            className="hover:underline"
          >
            agentskills.io
          </Link>
        </nav>
      </div>
    </footer>
  );
}
