"use client";

import * as React from "react";
import { m } from "framer-motion";
import { ScrollText, Workflow, Bot, Webhook } from "lucide-react";
import { ForgeMotion } from "@/components/motion/lazy-motion-provider";
import { usePrefersReducedMotion } from "@/components/motion/use-reduced-motion";
import { cn } from "@/lib/utils";

/**
 * PrimitiveCards — the four primitive kinds as interactive cards.
 *
 * Pointer-tracking tilt via framer-motion (the one place spring/gesture truly
 * improves feel). Loaded through next/dynamic by the page so framer stays OFF
 * the landing's critical bundle (portal-motion-system code-split budget), and
 * disabled entirely under prefers-reduced-motion. Content is plain text so it
 * is meaningful even before/without hydration.
 */
interface PrimitiveCopy {
  skillTitle: string;
  skillBody: string;
  ruleTitle: string;
  ruleBody: string;
  agentTitle: string;
  agentBody: string;
  hookTitle: string;
  hookBody: string;
}

export default function PrimitiveCards({ copy }: { copy: PrimitiveCopy }) {
  const items = [
    { Icon: ScrollText, title: copy.skillTitle, body: copy.skillBody },
    { Icon: Workflow, title: copy.ruleTitle, body: copy.ruleBody },
    { Icon: Bot, title: copy.agentTitle, body: copy.agentBody },
    { Icon: Webhook, title: copy.hookTitle, body: copy.hookBody },
  ];

  return (
    <ForgeMotion>
      <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
        {items.map((it) => (
          <TiltCard key={it.title} {...it} />
        ))}
      </div>
    </ForgeMotion>
  );
}

function TiltCard({
  Icon,
  title,
  body,
}: {
  Icon: React.ComponentType<{ className?: string }>;
  title: string;
  body: string;
}) {
  const reduced = usePrefersReducedMotion();
  const ref = React.useRef<HTMLDivElement | null>(null);
  const [tilt, setTilt] = React.useState({ rx: 0, ry: 0 });

  const onMove = (e: React.PointerEvent) => {
    if (reduced || !ref.current) return;
    const r = ref.current.getBoundingClientRect();
    const px = (e.clientX - r.left) / r.width - 0.5;
    const py = (e.clientY - r.top) / r.height - 0.5;
    setTilt({ rx: -py * 6, ry: px * 6 });
  };
  const reset = () => setTilt({ rx: 0, ry: 0 });

  return (
    <m.div
      ref={ref}
      onPointerMove={onMove}
      onPointerLeave={reset}
      animate={{ rotateX: tilt.rx, rotateY: tilt.ry }}
      transition={{ type: "spring", stiffness: 200, damping: 18 }}
      style={{ transformPerspective: 800 }}
      className={cn(
        "forge-glow-hover h-full rounded-lg border bg-card p-6 text-card-foreground shadow-sm"
      )}
    >
      <Icon className="h-7 w-7 text-ember" />
      <h3 className="mt-4 text-lg font-semibold tracking-tight">{title}</h3>
      <p className="mt-2 text-sm text-muted-foreground">{body}</p>
    </m.div>
  );
}
