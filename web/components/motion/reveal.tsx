"use client";

import * as React from "react";
import { cn } from "@/lib/utils";
import { usePrefersReducedMotion } from "./use-reduced-motion";

/**
 * Reveal — animates its children into view on scroll, once.
 *
 * Accessibility / no-JS contract (portal-motion-system spec):
 *   - The children are ALWAYS in the DOM and the server-rendered markup is in
 *     the *final* (visible) state. With JavaScript disabled the content simply
 *     stays visible — nothing is gated behind the animation.
 *   - On the client we "arm" the hidden state only after mount, then reveal
 *     when the element scrolls near the viewport (IntersectionObserver).
 *   - Under prefers-reduced-motion the element is never armed: it shows
 *     immediately with no transform/opacity transition.
 *
 * `delayMs` lets a parent stagger a list (pass an increasing delay per child).
 */
export function Reveal({
  children,
  className,
  delayMs = 0,
  /** translate distance for the entrance, in px. */
  y = 16,
  /** fraction of the element visible before it reveals. */
  threshold = 0.12,
}: {
  children: React.ReactNode;
  className?: string;
  delayMs?: number;
  y?: number;
  threshold?: number;
}) {
  const ref = React.useRef<HTMLDivElement | null>(null);
  const reduced = usePrefersReducedMotion();
  const [armed, setArmed] = React.useState(false);
  const [shown, setShown] = React.useState(false);

  React.useEffect(() => {
    if (reduced) {
      setArmed(false);
      setShown(true);
      return;
    }
    const el = ref.current;
    if (!el || typeof IntersectionObserver === "undefined") {
      setShown(true);
      return;
    }
    setArmed(true);
    const io = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            setShown(true);
            io.disconnect();
            break;
          }
        }
      },
      { threshold, rootMargin: "0px 0px -8% 0px" }
    );
    io.observe(el);
    return () => io.disconnect();
  }, [reduced, threshold]);

  return (
    <div
      ref={ref}
      className={cn(
        "transition-all duration-700 ease-out will-change-[opacity,transform]",
        armed && !shown ? "opacity-0" : "opacity-100",
        className
      )}
      style={
        armed && !shown
          ? { transform: `translateY(${y}px)`, transitionDelay: `${delayMs}ms` }
          : { transform: "translateY(0)", transitionDelay: `${delayMs}ms` }
      }
    >
      {children}
    </div>
  );
}
