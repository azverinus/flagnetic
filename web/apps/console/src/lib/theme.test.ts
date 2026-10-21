import { beforeEach, describe, expect, it } from "vitest";

/** The very script the built entry document loads before the first paint. */
import themeInit from "../../public/theme-init.js?raw";
import { applyTheme, readTheme, themeStorageKey, writeTheme } from "./theme";

const storage = globalThis.localStorage;

function prefersDark(dark: boolean) {
  window.matchMedia = ((query: string) =>
    ({
      matches: dark && query.includes("dark"),
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    }) as unknown as MediaQueryList) as typeof window.matchMedia;
}

beforeEach(() => {
  localStorage.clear();
  document.documentElement.className = "";
  delete document.documentElement.dataset["theme"];
  prefersDark(false);
});

describe("the theme", () => {
  it("is the system theme until the member chooses one", () => {
    expect(readTheme()).toBe("system");
  });

  it("survives a reload, because it is kept in browser storage", () => {
    writeTheme("dark");

    expect(localStorage.getItem(themeStorageKey)).toBe("dark");
    expect(readTheme()).toBe("dark");
  });

  it("ignores a stored value that is not a theme", () => {
    localStorage.setItem(themeStorageKey, "aubergine");

    expect(readTheme()).toBe("system");
  });

  it("puts the chosen theme on the document", () => {
    applyTheme("dark");

    expect(document.documentElement).toHaveClass("dark");
    expect(document.documentElement.dataset["theme"]).toBe("dark");
  });

  it("is the system theme, and choosing one does not throw, where the browser denies storage", () => {
    // A sandboxed frame or a blocked site throws on merely reaching
    // localStorage, before any of its methods is called.
    Object.defineProperty(globalThis, "localStorage", {
      configurable: true,
      get() {
        throw new DOMException("denied", "SecurityError");
      },
    });
    try {
      expect(readTheme()).toBe("system");
      expect(() => writeTheme("dark")).not.toThrow();
    } finally {
      Object.defineProperty(globalThis, "localStorage", { configurable: true, value: storage });
    }
  });

  it("follows the browser when the member chose system", () => {
    prefersDark(true);

    applyTheme("system");

    expect(document.documentElement).toHaveClass("dark");
  });
});

describe("the script that runs before the first paint", () => {
  it("applies the stored theme, so a dark member never sees a white flash", () => {
    localStorage.setItem(themeStorageKey, "dark");

    new Function(themeInit)();

    expect(document.documentElement).toHaveClass("dark");
    expect(document.documentElement.dataset["theme"]).toBe("dark");
  });

  it("falls back to what the browser prefers", () => {
    prefersDark(true);

    new Function(themeInit)();

    expect(document.documentElement).toHaveClass("dark");
  });

  it("agrees with the theme module it duplicates", () => {
    localStorage.setItem(themeStorageKey, "light");
    prefersDark(true);

    new Function(themeInit)();
    const fromTheScript = document.documentElement.className;
    document.documentElement.className = "";
    applyTheme(readTheme());

    expect(document.documentElement.className).toBe(fromTheScript);
  });
});
