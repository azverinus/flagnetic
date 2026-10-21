import { readStored, writeStored } from "./storage";

/**
 * The member's theme. It lives in browser storage rather than on the
 * member, so the sign-in page — which no session has reached yet — renders
 * in the theme they chose last.
 */
export type Theme = "light" | "dark" | "system";

/** Where the theme is kept, and what `public/theme-init.js` reads before the first paint. */
export const themeStorageKey = "flagnetic.theme";

export const themes: readonly Theme[] = ["light", "dark", "system"];

export function isTheme(value: unknown): value is Theme {
  return typeof value === "string" && (themes as readonly string[]).includes(value);
}

/** The stored theme, or `system` when nothing valid is stored or storage is denied. */
export function readTheme(): Theme {
  const stored = readStored(themeStorageKey);
  return isTheme(stored) ? stored : "system";
}

/** Stores the theme; a browser that denies storage keeps it for this page only. */
export function writeTheme(theme: Theme): void {
  writeStored(themeStorageKey, theme);
}

/** What `system` means right now. */
export function resolveTheme(theme: Theme, root: Document = document): "light" | "dark" {
  if (theme !== "system") return theme;
  return root.defaultView?.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

/** Puts the theme on the document, where the tokens hang off `.dark`. */
export function applyTheme(theme: Theme, root: Document = document): void {
  const resolved = resolveTheme(theme, root);
  root.documentElement.classList.toggle("dark", resolved === "dark");
  root.documentElement.dataset["theme"] = resolved;
  root.documentElement.style.colorScheme = resolved;
}
