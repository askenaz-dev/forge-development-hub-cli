"use server";

import { signOut } from "@/auth";

/**
 * Server action that clears the Auth.js session cookie and returns the user to
 * the landing page. Passed to the (client) user-menu / mobile-nav so the
 * sign-out button is a plain progressive-enhancement `<form action={...}>` —
 * no client-side session library required.
 */
export async function signOutAction(): Promise<void> {
  await signOut({ redirectTo: "/" });
}
