import type { Me } from "@flagnetic/api";
import { i18n } from "@lingui/core";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { localeStorageKey } from "@/i18n/locale";
import { activateForTests } from "@/test/render";

import { followMemberLocale, memberLocale } from "./member-locale";

function meWith(locale: string | null): Me {
  return {
    member: {
      id: "0192f0c4-0000-7000-8000-000000000001",
      email: "ada@example.com",
      display_name: "Ada",
      status: "active",
      instance_admin: false,
      locale,
    },
    credential: { method: "session" },
    client_ip: "127.0.0.1",
    max_token_lifetime_seconds: 31_536_000,
    require_token_expiry: false,
  };
}

beforeEach(() => {
  localStorage.clear();
  vi.spyOn(navigator, "languages", "get").mockReturnValue(["en-US"]);
});

afterEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
  activateForTests("en");
});

describe("the console's language for a signed-in member", () => {
  it("is the member's choice, remembered in this browser for the sign-in page", async () => {
    localStorage.setItem(localeStorageKey, "en");
    expect(memberLocale(meWith("ru"))).toBe("ru");
    expect(localStorage.getItem(localeStorageKey)).toBe("en");
    await followMemberLocale(i18n, meWith("ru"));
    expect(localStorage.getItem(localeStorageKey)).toBe("ru");
  });

  it("follows the browser for a member who chose none, forgetting what was remembered", async () => {
    localStorage.setItem(localeStorageKey, "ru");
    expect(memberLocale(meWith(null))).toBe("en");
    await followMemberLocale(i18n, meWith(null));
    expect(localStorage.getItem(localeStorageKey)).toBeNull();
  });

  it("leaves what this browser remembers alone for a project token, which acts for no member", async () => {
    localStorage.setItem(localeStorageKey, "ru");
    const me: Me = {
      credential: { method: "token" },
      client_ip: "127.0.0.1",
      max_token_lifetime_seconds: null,
      require_token_expiry: false,
    };
    expect(memberLocale(me)).toBe("ru");
    await followMemberLocale(i18n, me);
    expect(localStorage.getItem(localeStorageKey)).toBe("ru");
  });

  it("writes to this browser only when what it remembers differs", async () => {
    localStorage.setItem(localeStorageKey, "ru");
    const setItem = vi.spyOn(Storage.prototype, "setItem");
    await followMemberLocale(i18n, meWith("ru"));
    await followMemberLocale(i18n, meWith("ru"));
    expect(setItem).not.toHaveBeenCalled();
  });

  it("switches the rendered language when the member's choice differs from it", async () => {
    activateForTests("en");
    await followMemberLocale(i18n, meWith("ru"));
    expect(i18n.locale).toBe("ru");
    expect(document.documentElement.lang).toBe("ru");
  });
});
