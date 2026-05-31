"use client";

import * as React from "react";

/**
 * usePrefersReducedMotion — reads the `prefers-reduced-motion` media query
 * and stays in sync if the user changes the OS setting at runtime.
 *
 * SSR-safe: returns `false` on the server and on first client render, then
 * corrects after mount. Callers MUST treat `true` as "show final state, no
 * tweening" so that content is never gated behind an animation.
 *
 * This is the single source of truth for the JS motion path; the CSS path is
 * backstopped by the global `@media (prefers-reduced-motion: reduce)` guard in
 * globals.css. Keeping our own tiny hook (instead of framer's) means the
 * lightweight primitives (Reveal, CountUp) pull in zero animation-library JS.
 */
export function usePrefersReducedMotion(): boolean {
  const [reduced, setReduced] = React.useState(false);

  React.useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
    setReduced(mq.matches);
    const onChange = (e: MediaQueryListEvent) => setReduced(e.matches);
    // addEventListener is supported in all evergreen browsers; the older
    // addListener fallback keeps Safari < 14 working.
    if (mq.addEventListener) {
      mq.addEventListener("change", onChange);
      return () => mq.removeEventListener("change", onChange);
    }
    mq.addListener(onChange);
    return () => mq.removeListener(onChange);
  }, []);

  return reduced;
}
