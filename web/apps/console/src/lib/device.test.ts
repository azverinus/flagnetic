import { i18n } from "@lingui/core";
import { beforeEach, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import { deviceName, deviceOf } from "./device";

beforeEach(() => activateForTests("en"));

describe("the device a session is on", () => {
  it("is named by its browser and system", () => {
    const chromeMac =
      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36";
    expect(deviceOf(chromeMac)).toEqual({ browser: "Chrome", os: "macOS" });
    expect(deviceName(i18n, chromeMac)).toBe("Chrome on macOS");
    expect(
      deviceOf(
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36 Edg/140.0",
      ),
    ).toEqual({ browser: "Edge", os: "Windows" });
    expect(
      deviceOf(
        "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
      ),
    ).toEqual({ browser: "Safari", os: "iOS" });
    expect(
      deviceOf("Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0"),
    ).toEqual({
      browser: "Firefox",
      os: "Linux",
    });
  });

  it("falls back to the start of what the client sent", () => {
    expect(deviceName(i18n, "Go-http-client/1.1")).toBe("Go-http-client/1.1");
    expect(deviceName(i18n, "")).toBe("Unknown device");
  });
});
