/**
 * The browser's local storage, carrying on when the browser denies it: a
 * preference is then simply not remembered. Every preference the console
 * keeps goes through here, because a sandboxed frame or a site whose
 * storage is blocked throws on merely reaching `localStorage`, and a throw
 * before the first render would leave the page blank.
 */
function browserStorage(): Storage | undefined {
  try {
    return globalThis.localStorage;
  } catch {
    return undefined;
  }
}

/** The stored value, or null when there is none or storage is denied. */
export function readStored(key: string): string | null {
  try {
    return browserStorage()?.getItem(key) ?? null;
  } catch {
    return null;
  }
}

/** Stores the value; null forgets it. */
export function writeStored(key: string, value: string | null): void {
  try {
    const storage = browserStorage();
    if (value === null) storage?.removeItem(key);
    else storage?.setItem(key, value);
  } catch {
    // Denied storage: the preference lasts until the page is left.
  }
}
