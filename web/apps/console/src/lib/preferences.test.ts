import { afterEach, describe, expect, it, vi } from "vitest";

import { rememberSidebarCollapsed, sidebarCollapsed } from "./preferences";

afterEach(() => {
  vi.restoreAllMocks();
});

describe("what the console remembers in this browser", () => {
  it("remembers the sidebar collapsed and expanded", () => {
    expect(sidebarCollapsed()).toBe(false);
    rememberSidebarCollapsed(true);
    expect(sidebarCollapsed()).toBe(true);
    rememberSidebarCollapsed(false);
    expect(sidebarCollapsed()).toBe(false);
  });

  it("carries on when the browser denies storage", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("denied", "SecurityError");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("denied", "SecurityError");
    });
    expect(() => rememberSidebarCollapsed(true)).not.toThrow();
    expect(sidebarCollapsed()).toBe(false);
  });
});
