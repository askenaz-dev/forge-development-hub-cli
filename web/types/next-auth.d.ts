/**
 * Module-augmentation for Auth.js (NextAuth) types.
 *
 * The augmented fields are populated in `auth.ts` callbacks and consumed
 * by every server component that reads `auth()`.
 */
import "next-auth";
import "next-auth/jwt";

declare module "next-auth" {
  interface Session {
    accessToken?: string;
    user?: {
      name?: string | null;
      email?: string | null;
      image?: string | null;
      groups?: string[];
      preferredUsername?: string;
    };
  }
}

declare module "next-auth/jwt" {
  interface JWT {
    accessToken?: string;
    idToken?: string;
    expiresAt?: number;
    groups?: string[];
    preferredUsername?: string;
  }
}
