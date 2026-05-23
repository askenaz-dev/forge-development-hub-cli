"use client";

import * as React from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeHighlight from "rehype-highlight";

/**
 * MarkdownView renders a Markdown string with GFM + syntax highlighting.
 *
 * The component is a client island because rehype-highlight runs in the
 * browser. For SEO-critical content we'd render server-side; for SKILL.md
 * bodies the content is auxiliary, so client rendering is fine and keeps
 * the server simple.
 */
export function MarkdownView({ markdown }: { markdown: string }) {
  return (
    <div className="prose prose-slate dark:prose-invert max-w-none">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeHighlight]}
      >
        {markdown}
      </ReactMarkdown>
    </div>
  );
}
