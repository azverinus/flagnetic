import { afterEach, describe, expect, it, vi } from "vitest";

import {
  instanceDefaultLocale,
  localeStorageKey,
  matchLocale,
  resolveLocale,
  storedLocale,
} from "./locale";

afterEach(() => {
  vi.restoreAllMocks();
  document.head.innerHTML = "";
});

describe("the language the console renders in", () => {
  it("is the member's choice before anything else", () => {
    expect(
      resolveLocale({ member: "ru", stored: "en", browser: ["en-US"], instanceDefault: "en" }),
    ).toBe("ru");
  });

  it("is the one this browser remembers when the member follows the browser", () => {
    expect(resolveLocale({ member: null, stored: "ru", browser: ["en-US"] })).toBe("ru");
  });

  it("is the first of the browser's languages the console offers", () => {
    expect(resolveLocale({ browser: ["de-DE", "ru-RU", "en"] })).toBe("ru");
    expect(resolveLocale({ browser: ["en-GB", "ru"] })).toBe("en");
  });

  it("is the instance's default when the browser asks for none the console offers", () => {
    expect(resolveLocale({ browser: ["de-DE", "fr"], instanceDefault: "ru" })).toBe("ru");
  });

  it("is English when nothing names a language the console offers", () => {
    expect(resolveLocale({ stored: "klingon", browser: [], instanceDefault: "de" })).toBe("en");
    expect(resolveLocale({})).toBe("en");
  });

  it("matches a regional tag by its language", () => {
    expect(matchLocale("ru-RU")).toBe("ru");
    expect(matchLocale("EN-us")).toBe("en");
    expect(matchLocale("pt-BR")).toBeUndefined();
  });

  it("reads the instance's default from the page the server wrote", () => {
    document.head.innerHTML = '<meta name="flagnetic-default-locale" content="ru">';
    expect(instanceDefaultLocale()).toBe("ru");
  });

  it("is read from what this browser remembers", () => {
    expect(storedLocale()).toBeNull();
    localStorage.setItem(localeStorageKey, "ru");
    expect(storedLocale()).toBe("ru");
    localStorage.removeItem(localeStorageKey);
  });

  it("carries on when the browser denies storage", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("denied", "SecurityError");
    });
    expect(storedLocale()).toBeNull();
  });
});
