import type { ScimToken } from "@flagnetic/api";
import { describe, expect, it } from "vitest";

import { scimBaseUrl, scimTokenState } from "./scim";

const token: ScimToken = {
  id: "0192f0c4-0000-7000-8000-000000000020",
  provider: "acme",
  hint: "fn_scim_Ab3d",
  expires_at: null,
  created_at: "2026-11-01T00:00:00Z",
  last_used_at: null,
  revoked_at: null,
};

describe("scimTokenState", () => {
  const now = new Date("2026-11-10T00:00:00Z");

  it("is active while it neither expired nor was revoked", () => {
    expect(scimTokenState(token, now)).toBe("active");
    expect(scimTokenState({ ...token, expires_at: "2026-12-01T00:00:00Z" }, now)).toBe("active");
  });

  it("is expired from its expiry on", () => {
    expect(scimTokenState({ ...token, expires_at: "2026-11-10T00:00:00Z" }, now)).toBe("expired");
  });

  it("is revoked whatever its expiry", () => {
    expect(
      scimTokenState(
        { ...token, expires_at: "2026-01-01T00:00:00Z", revoked_at: "2026-02-01T00:00:00Z" },
        now,
      ),
    ).toBe("revoked");
  });
});

describe("scimBaseUrl", () => {
  it("is /scim/v2 on the console's origin", () => {
    expect(scimBaseUrl("https://flags.example.com")).toBe("https://flags.example.com/scim/v2");
  });
});
