"use client";

import * as React from "react";
import { ThemeProvider as NextThemesProvider, type ThemeProviderProps } from "next-themes";

/**
 * ThemeProvider wraps next-themes with FDH-portal defaults:
 *   - light / dark / system modes
 *   - no flash on first paint (class strategy + suppressHydrationWarning)
 *   - persisted to localStorage under the key `fdh-theme`
 */
export function ThemeProvider({ children, ...props }: ThemeProviderProps) {
  return (
    <NextThemesProvider
      attribute="class"
      defaultTheme="system"
      enableSystem
      storageKey="fdh-theme"
      disableTransitionOnChange
      {...props}
    >
      {children}
    </NextThemesProvider>
  );
}
