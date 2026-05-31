"use client";

import * as React from "react";
import { usePrefersReducedMotion } from "./use-reduced-motion";

/**
 * CountUp — animates a number from 0 to `to` once it scrolls into view, then
 * lands on EXACTLY `to` (never an approximation).
 *
 * Accessibility (portal-motion-system spec):
 *   - The animated digits are aria-hidden; a visually-hidden span carries the
 *     final value so screen readers announce `to`, not the intermediate frames.
 *   - Under prefers-reduced-motion the final value is shown immediately with
 *     no tween.
 */
export function CountUp({
  to,
  durationMs = 1200,
  prefix = "",
  suffix = "",
  className,
}: {
  to: number;
  durationMs?: number;
  prefix?: string;
  suffix?: string;
  className?: string;
}) {
  const reduced = usePrefersReducedMotion();
  const ref = React.useRef<HTMLSpanElement | null>(null);
  const [value, setValue] = React.useState(0);

  React.useEffect(() => {
    if (reduced) {
      setValue(to);
      return;
    }
    const el = ref.current;
    if (!el || typeof IntersectionObserver === "undefined") {
      setValue(to);
      return;
    }
    let raf = 0;
    let started = false;
    const animate = () => {
      const start = performance.now();
      const tick = (now: number) => {
        const p = Math.min(1, (now - start) / durationMs);
        const eased = 1 - Math.pow(1 - p, 3); // easeOutCubic
        setValue(Math.round(eased * to));
        if (p < 1) {
          raf = requestAnimationFrame(tick);
        } else {
          setValue(to); // guarantee exact landing
        }
      };
      raf = requestAnimationFrame(tick);
    };
    const io = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting && !started) {
            started = true;
            animate();
            io.disconnect();
            break;
          }
        }
      },
      { threshold: 0.5 }
    );
    io.observe(el);
    return () => {
      io.disconnect();
      cancelAnimationFrame(raf);
    };
  }, [reduced, to, durationMs]);

  return (
    <span ref={ref} className={className}>
      <span aria-hidden="true">
        {prefix}
        {value}
        {suffix}
      </span>
      <span className="sr-only">
        {prefix}
        {to}
        {suffix}
      </span>
    </span>
  );
}
