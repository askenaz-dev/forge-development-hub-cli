"use client";

import * as React from "react";
import { Check, Copy } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/**
 * CopyCommand renders a one-line command in a code block with a Copy
 * button that puts the exact command on the clipboard (no leading prompt,
 * no trailing newline). On click, the button shows a brief "Copied!"
 * state, then reverts after 2 seconds.
 *
 * Used on the install page and on every skill detail page.
 */
export function CopyCommand({
  command,
  className,
  label = "Copy command",
}: {
  command: string;
  className?: string;
  label?: string;
}) {
  const [copied, setCopied] = React.useState(false);

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      // Permission denied or document not focused — degrade gracefully.
      setCopied(false);
    }
  }

  return (
    <div
      className={cn(
        "group relative flex items-center gap-2 rounded-md border bg-muted/50 px-3 py-2 font-mono text-sm",
        className
      )}
    >
      <code
        // Scrollable region needs keyboard focus so non-pointer users can
        // scroll long commands horizontally with arrow keys (WCAG 2.1.1).
        tabIndex={0}
        className="flex-1 overflow-x-auto whitespace-pre focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring rounded-sm"
      >
        {command}
      </code>
      <Button
        type="button"
        size="icon"
        variant="ghost"
        aria-label={label}
        onClick={handleCopy}
        className="shrink-0"
      >
        {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
      </Button>
    </div>
  );
}
