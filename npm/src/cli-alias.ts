// Entry point for the `falabella-installer` bin alias.
//
// `falabella-installer` was the original CLI name before `dev-portal` renamed
// it to `fdh` (90-day deprecation stub in `cmd/falabella-installer-stub`).
// This npm-side alias preserves backward compatibility for scripts and docs
// that haven't migrated yet. It prints a one-line warning to stderr and then
// behaves identically to `fdh`.
//
// Tracking: stub is scheduled for removal alongside the corresponding Go stub
// in `cmd/falabella-installer-stub`.

import { runFdh } from "./cli.js";

const DEPRECATION_TARGET_DATE = "2026-08-21"; // 90 days from dev-portal apply

console.warn(
  `\nfalabella-installer: deprecated name, please use \`fdh\` instead.\n` +
    `This alias will be removed on or after ${DEPRECATION_TARGET_DATE}.\n` +
    `To migrate: replace \`falabella-installer\` with \`fdh\` in your scripts/docs.\n`,
);

runFdh();
