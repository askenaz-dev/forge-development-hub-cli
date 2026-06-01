"use client";

import { useState } from "react";
import { postEvent, type Kind } from "@/lib/api";

/**
 * FeedbackWidget is the voluntary (Tier 2) feedback affordance on a component
 * detail page: a thumbs up/down plus an optional short message. It posts
 * `feedback.submitted` through the same-origin BFF route. Nothing is sent
 * until the user acts — this is the only path that transmits free-form text,
 * and only because the user typed it.
 */
export function FeedbackWidget({
  kind,
  namespace,
  name,
}: {
  kind: Kind;
  namespace: string;
  name: string;
}) {
  const [sentiment, setSentiment] = useState<"up" | "down" | null>(null);
  const [message, setMessage] = useState("");
  const [sent, setSent] = useState(false);

  async function submit(s: "up" | "down") {
    setSentiment(s);
    const attributes: Record<string, string> = {
      kind,
      namespace,
      name,
      sentiment: s,
      surface: "web",
    };
    if (message.trim()) attributes.text = message.trim();
    await postEvent({ event_name: "feedback.submitted", attributes });
    setSent(true);
  }

  if (sent) {
    return (
      <p className="text-sm text-muted-foreground" role="status">
        Thanks for the feedback!
      </p>
    );
  }

  return (
    <div className="space-y-3">
      <p className="text-sm font-medium">Was this component useful?</p>
      <div className="flex gap-2">
        <button
          type="button"
          aria-label="thumbs up"
          onClick={() => submit("up")}
          className={`rounded-md border px-3 py-1 text-sm hover:bg-muted ${
            sentiment === "up" ? "bg-muted" : ""
          }`}
        >
          👍 Yes
        </button>
        <button
          type="button"
          aria-label="thumbs down"
          onClick={() => submit("down")}
          className={`rounded-md border px-3 py-1 text-sm hover:bg-muted ${
            sentiment === "down" ? "bg-muted" : ""
          }`}
        >
          👎 No
        </button>
      </div>
      <textarea
        value={message}
        onChange={(e) => setMessage(e.target.value)}
        placeholder="Optional: what would make it better? (no code or secrets, please)"
        rows={2}
        maxLength={2000}
        className="w-full rounded-md border bg-background p-2 text-sm"
      />
    </div>
  );
}
