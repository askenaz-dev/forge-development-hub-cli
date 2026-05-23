import { handlers } from "@/auth";

// Auth.js v5 exposes the route handlers as `handlers` from the central
// `auth.ts` config. The dynamic catch-all path `[...nextauth]` covers
// sign-in, callback, and sign-out routes.
export const { GET, POST } = handlers;
