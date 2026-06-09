import "server-only";

/**
 * Deterministic avatar derivation — SERVER ONLY (uses `node:crypto`).
 *
 * The profile avatar is derived deterministically from the user's email; there
 * is NO upload affordance in Phase 1 (upload is Phase 2 USER-DATA, per
 * design.md D5). Two providers are supported, selected by the deployment env
 * var `PORTAL_AVATAR_PROVIDER`:
 *
 *   - "gravatar" (default) — `https://www.gravatar.com/avatar/<sha256(email)>?d=identicon`.
 *     `d=identicon` guarantees a deterministic generated image even when the
 *     user has no Gravatar account (never a broken image). The email is
 *     lowercased + trimmed before hashing (Gravatar's canonicalization, here
 *     with SHA-256 per the spec rather than the legacy MD5).
 *
 *   - "local" — a fully offline initials avatar (an inline data-URI SVG). It
 *     makes NO outbound request, for privacy-strict or air-gapped deployments.
 *
 * CSP note: when (and only when) the gravatar provider is active, the portal
 * CSP `img-src` must permit `https://www.gravatar.com` (wired in
 * `next.config.mjs` `headers()`, scoped to `/profile`). The local provider
 * embeds the image as a `data:` URI, which the same CSP already allows.
 */

import { createHash } from "node:crypto";

/** The Gravatar origin the CSP `img-src` must allow when this provider is active. */
export const GRAVATAR_ORIGIN = "https://www.gravatar.com";

export type AvatarProvider = "gravatar" | "local";

/**
 * avatarProvider resolves the configured provider. Anything other than the
 * explicit opt-out `local` (case-insensitive) keeps the Gravatar default, so a
 * typo never silently disables avatars — it just falls back to the default.
 */
export function avatarProvider(): AvatarProvider {
  return process.env.PORTAL_AVATAR_PROVIDER?.toLowerCase() === "local"
    ? "local"
    : "gravatar";
}

/** SHA-256 hex of the lowercased, trimmed email (Gravatar canonicalization). */
function emailHash(email: string): string {
  return createHash("sha256")
    .update(email.trim().toLowerCase())
    .digest("hex");
}

/** The Gravatar avatar URL for an email, with `d=identicon` as the default image. */
export function gravatarUrl(email: string): string {
  return `${GRAVATAR_ORIGIN}/avatar/${emailHash(email)}?d=identicon`;
}

/**
 * Derive 1–2 uppercase initials from a display name (preferred) or email.
 * "Ada Lovelace" -> "AL"; "ada@example.com" -> "A"; empty -> "?".
 */
export function initialsFor(name?: string | null, email?: string | null): string {
  const source = (name ?? "").trim() || (email ?? "").trim();
  if (!source) return "?";
  // For an email, only the local-part before "@" is meaningful for initials.
  const base = source.includes("@") ? source.split("@")[0] ?? source : source;
  const words = base.split(/[\s._-]+/).filter(Boolean);
  const first = words[0];
  const last = words[words.length - 1];
  if (!first) return "?";
  if (words.length === 1 || !last) {
    return first.slice(0, 1).toUpperCase();
  }
  return (first.charAt(0) + last.charAt(0)).toUpperCase();
}

/**
 * Map an arbitrary string to a stable hue (0–359) so each user gets a
 * consistent background color in the local avatar. Deterministic, no I/O.
 */
function stableHue(seed: string): number {
  let h = 0;
  for (let i = 0; i < seed.length; i += 1) {
    h = (h * 31 + seed.charCodeAt(i)) % 360;
  }
  return h;
}

/**
 * localAvatarDataUri renders an offline initials avatar as a `data:` URI SVG.
 * No outbound request is ever made. The background hue is derived from the
 * email (or name) so the image is deterministic per user.
 */
export function localAvatarDataUri(
  name?: string | null,
  email?: string | null
): string {
  const initials = initialsFor(name, email);
  const hue = stableHue((email ?? name ?? "").trim().toLowerCase() || "anon");
  const bg = `hsl(${hue} 55% 45%)`;
  const svg = [
    `<svg xmlns="http://www.w3.org/2000/svg" width="96" height="96" viewBox="0 0 96 96" role="img">`,
    `<rect width="96" height="96" rx="12" fill="${bg}"/>`,
    `<text x="50%" y="50%" dy=".35em" text-anchor="middle" `,
    `font-family="ui-sans-serif,system-ui,sans-serif" font-size="40" `,
    `font-weight="600" fill="#ffffff">${initials}</text>`,
    `</svg>`,
  ].join("");
  // `encodeURIComponent` keeps the data URI valid for any initials/charset.
  return `data:image/svg+xml;utf8,${encodeURIComponent(svg)}`;
}

/**
 * resolveAvatar returns everything the profile needs to render the avatar:
 * the `src` to use and which provider produced it. The caller renders an
 * `<img>` with this `src`; alt text and initials fallback live in the view.
 */
export function resolveAvatar(
  name?: string | null,
  email?: string | null
): { src: string; provider: AvatarProvider; initials: string } {
  const provider = avatarProvider();
  const initials = initialsFor(name, email);
  if (provider === "local" || !email || !email.trim()) {
    // No email -> we cannot hit Gravatar meaningfully; use the local image so
    // the avatar is never broken.
    return { src: localAvatarDataUri(name, email), provider: "local", initials };
  }
  return { src: gravatarUrl(email), provider: "gravatar", initials };
}
