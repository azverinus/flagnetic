import { useCallback, useEffect, useSyncExternalStore } from "react";

import { applyTheme, readTheme, type Theme, themeStorageKey, writeTheme } from "./theme";

/**
 * Announced on the window whenever the member picks a theme, so every
 * control showing it follows, the toasts included, which render outside
 * the router.
 */
export const themeEvent = "flagnetic:theme";

/** The theme picked on this page when the browser denied storing it. */
let unstored: Theme | undefined;

function subscribe(onChange: () => void): () => void {
  // Another tab picked one: this page follows it.
  const fromOtherTab = (event: StorageEvent) => {
    if (event.key !== null && event.key !== themeStorageKey) return;
    unstored = undefined;
    applyTheme(readTheme());
    onChange();
  };
  window.addEventListener(themeEvent, onChange);
  window.addEventListener("storage", fromOtherTab);
  return () => {
    window.removeEventListener(themeEvent, onChange);
    window.removeEventListener("storage", fromOtherTab);
  };
}

function snapshot(): Theme {
  return unstored ?? readTheme();
}

/** The member's theme and a way to change it, kept in browser storage. */
export function useTheme(): [Theme, (theme: Theme) => void] {
  const theme = useSyncExternalStore(subscribe, snapshot);

  // While the member follows the system, the system may change under them.
  useEffect(() => {
    if (theme !== "system") return;
    const query = window.matchMedia("(prefers-color-scheme: dark)");
    const update = () => applyTheme("system");
    query.addEventListener("change", update);
    return () => query.removeEventListener("change", update);
  }, [theme]);

  const choose = useCallback((value: Theme) => {
    writeTheme(value);
    unstored = readTheme() === value ? undefined : value;
    applyTheme(value);
    window.dispatchEvent(new Event(themeEvent));
  }, []);

  return [theme, choose];
}
