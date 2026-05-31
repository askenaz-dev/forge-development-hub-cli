"use client";

import { LazyMotion, domAnimation } from "framer-motion";
import * as React from "react";

/**
 * ForgeMotion — wraps interactive framer-motion subtrees in LazyMotion with
 * the `domAnimation` feature bundle.
 *
 * Why: framer's full feature set is ~45KB. `LazyMotion` + `domAnimation` ships
 * a much smaller subset and, combined with importing `m` (not `motion`), keeps
 * the animation library OUT of the landing's critical bundle. Only components
 * that actually use motion (primitive-card tilt, the how-it-works stepper) wrap
 * themselves in this provider, and they are dynamically imported so the JS is
 * loaded on demand — satisfying the portal-motion-system code-split budget.
 *
 * Use the `m.*` components (e.g. `m.div`) inside, never `motion.*`.
 */
export function ForgeMotion({ children }: { children: React.ReactNode }) {
  return <LazyMotion features={domAnimation} strict>{children}</LazyMotion>;
}
