#!/usr/bin/env node
/**
 * Translation-parity check.
 *
 * Fails (exit 1) when the dotted-key sets of `messages/es.json` and
 * `messages/en.json` differ. Prints every missing key under each locale
 * so the developer can fix the gap without manual diffing.
 *
 * Usage:
 *   node scripts/check-i18n-parity.mjs
 *
 * Or via the npm/pnpm script:
 *   pnpm i18n:check
 *
 * Adding a third locale: just drop messages/<code>.json next to the
 * existing files. The script auto-discovers every JSON file under
 * messages/ and checks every pair.
 */

import { readFileSync, readdirSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const messagesDir = join(__dirname, "..", "messages");

/** Recursively collect every dotted-key path of a parsed JSON object. */
function collectKeys(obj, prefix = "") {
  const out = new Set();
  if (obj === null || typeof obj !== "object" || Array.isArray(obj)) {
    if (prefix) out.add(prefix);
    return out;
  }
  for (const [k, v] of Object.entries(obj)) {
    const next = prefix ? `${prefix}.${k}` : k;
    if (v !== null && typeof v === "object" && !Array.isArray(v)) {
      for (const child of collectKeys(v, next)) out.add(child);
    } else {
      out.add(next);
    }
  }
  return out;
}

function loadLocale(file) {
  const raw = readFileSync(join(messagesDir, file), "utf8");
  const parsed = JSON.parse(raw);
  return collectKeys(parsed);
}

function difference(a, b) {
  return [...a].filter((k) => !b.has(k)).sort();
}

const localeFiles = readdirSync(messagesDir).filter((f) => f.endsWith(".json"));
if (localeFiles.length < 2) {
  console.log(`only ${localeFiles.length} locale file(s) present — nothing to compare`);
  process.exit(0);
}

const sets = new Map();
for (const file of localeFiles) {
  sets.set(file.replace(/\.json$/, ""), loadLocale(file));
}

let problems = 0;
const locales = [...sets.keys()];
for (let i = 0; i < locales.length; i++) {
  for (let j = i + 1; j < locales.length; j++) {
    const a = locales[i];
    const b = locales[j];
    const inAOnly = difference(sets.get(a), sets.get(b));
    const inBOnly = difference(sets.get(b), sets.get(a));
    if (inAOnly.length === 0 && inBOnly.length === 0) continue;
    problems += inAOnly.length + inBOnly.length;
    console.error(`\n  ${a} ↔ ${b}: ${inAOnly.length + inBOnly.length} key(s) differ`);
    for (const k of inAOnly) console.error(`    only in ${a}: ${k}`);
    for (const k of inBOnly) console.error(`    only in ${b}: ${k}`);
  }
}

if (problems > 0) {
  console.error(`\ni18n parity check FAILED — ${problems} mismatched key(s).`);
  process.exit(1);
}
console.log(`i18n parity check OK — ${localeFiles.length} locale(s) agree.`);
