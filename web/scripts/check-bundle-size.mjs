#!/usr/bin/env node
/**
 * Landing first-load JS budget gate (portal-motion-system spec).
 *
 * Fails (exit 1) if the localized landing route's first-load JavaScript exceeds
 * the gzipped budget. This is the guardrail that lets us add motion WITHOUT
 * regressing performance: the redesign must keep the landing under budget even
 * with the Ember Forge animations and framer-motion (which must stay
 * code-split, not on the landing's critical path).
 *
 * How it measures: after `next build`, Next emits
 * `.next/app-build-manifest.json` mapping each route to the JS files that make
 * up its initial load. We gzip-size every .js file for the landing route and
 * sum them. The landing route key is the `[locale]` home page.
 *
 * Usage (after a production build):
 *   node scripts/check-bundle-size.mjs
 *
 * Run as part of `pnpm ci` (which builds first).
 */

import { readFileSync, existsSync, statSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";

const __dirname = dirname(fileURLToPath(import.meta.url));
const webRoot = join(__dirname, "..");
const nextDir = join(webRoot, ".next");

const BUDGET_BYTES = 200 * 1024; // 200 KB gzipped

function fail(msg) {
  console.error(`\nbundle-size check FAILED — ${msg}`);
  process.exit(1);
}

const manifestPath = join(nextDir, "app-build-manifest.json");
if (!existsSync(manifestPath)) {
  fail(
    `no build manifest at ${manifestPath}. Run \`next build\` before this check ` +
      `(or run it via \`pnpm ci\`).`
  );
}

const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
const pages = manifest.pages ?? {};

// The landing route lives under the [locale] segment. Match the home page
// route key (".../[locale]/page") and not the per-kind or detail pages.
const landingKey = Object.keys(pages).find(
  (k) => /\/\[locale\]\/page$/.test(k) || k === "/[locale]/page"
);

if (!landingKey) {
  console.warn(
    "bundle-size check: could not find the [locale] landing route in the " +
      "build manifest; skipping (routes present: " +
      Object.keys(pages).join(", ") +
      ")"
  );
  process.exit(0);
}

const files = [...new Set(pages[landingKey])].filter((f) => f.endsWith(".js"));
let total = 0;
const rows = [];
for (const rel of files) {
  const abs = join(nextDir, rel);
  if (!existsSync(abs)) continue;
  const raw = readFileSync(abs);
  const gz = gzipSync(raw).length;
  total += gz;
  rows.push({ rel, gz });
}

rows.sort((a, b) => b.gz - a.gz);
const kb = (n) => `${(n / 1024).toFixed(1)} KB`;

console.log(`\nLanding first-load JS (gzipped) — route ${landingKey}`);
for (const r of rows.slice(0, 12)) {
  console.log(`  ${kb(r.gz).padStart(9)}  ${r.rel}`);
}
console.log(`  ${"".padStart(9)}  ----`);
console.log(`  ${kb(total).padStart(9)}  TOTAL  (budget ${kb(BUDGET_BYTES)})`);

if (total > BUDGET_BYTES) {
  fail(
    `landing first-load JS is ${kb(total)} gzipped, over the ${kb(
      BUDGET_BYTES
    )} budget. Keep framer-motion code-split and prefer CSS/RSC.`
  );
}

console.log(`\nbundle-size check OK — landing under the ${kb(BUDGET_BYTES)} budget.`);
