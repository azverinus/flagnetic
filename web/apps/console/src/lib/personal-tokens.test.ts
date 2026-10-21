import type { ApiToken, Me } from "@flagnetic/api";
import { describe, expect, it } from "vitest";

import {
  defaultExpiry,
  expiresAt,
  expiryChoices,
  offeredScopes,
  type TokenPolicy,
  tokenPolicy,
  tokenState,
  withImpliedRead,
} from "./personal-tokens";

const day = 24 * 60 * 60 * 1000;
const now = new Date("2026-10-21T12:00:00Z");

describe("the scopes of a new personal access token", () => {
  it("offer instance:admin to an instance admin alone", () => {
    expect(offeredScopes(false)).toEqual([
      "read",
      "flags:write",
      "experiments:write",
      "project:admin",
    ]);
    expect(offeredScopes(true)).toContain("instance:admin");
  });

  it("always carry read beside a write scope, in the offered order", () => {
    expect(withImpliedRead(["project:admin", "flags:write"])).toEqual([
      "read",
      "flags:write",
      "project:admin",
    ]);
    expect(withImpliedRead(["read"])).toEqual(["read"]);
    expect(withImpliedRead(["instance:admin"])).toEqual(["read", "instance:admin"]);
    expect(withImpliedRead([])).toEqual([]);
  });
});

const unbounded: TokenPolicy = { expiryRequired: false };

describe("the instance's token policy", () => {
  it("is what GET /me answers, null meaning no bound", () => {
    const me: Me = {
      credential: { method: "session" },
      client_ip: "127.0.0.1",
      max_token_lifetime_seconds: 86_400,
      require_token_expiry: true,
    };
    expect(tokenPolicy(me)).toEqual({ maxLifetimeSeconds: 86_400, expiryRequired: true });
    expect(
      tokenPolicy({ ...me, max_token_lifetime_seconds: null, require_token_expiry: false }),
    ).toEqual(unbounded);
  });
});

describe("the expiry of a new personal access token", () => {
  it("offers every preset and never when the instance sets no bound", () => {
    const choices = expiryChoices(unbounded);
    expect(choices).toEqual(["7", "30", "90", "365", "never"]);
    expect(defaultExpiry(choices)).toBe("30");
  });

  it("offers only the presets within the instance's maximum lifetime, and never only when allowed", () => {
    const policy = { maxLifetimeSeconds: 60 * 24 * 60 * 60, expiryRequired: true };
    expect(expiryChoices(policy)).toEqual(["7", "30"]);
    const short = { maxLifetimeSeconds: 2 * 24 * 60 * 60, expiryRequired: true };
    expect(expiryChoices(short)).toEqual(["7"]);
    expect(defaultExpiry(expiryChoices(short))).toBe("7");
  });

  it("is the number of days chosen ahead, or none for never", () => {
    expect(expiresAt("30", now, unbounded)).toBe(new Date(now.getTime() + 30 * day).toISOString());
    expect(expiresAt("never", now, unbounded)).toBeNull();
  });

  it("stays inside the instance's bound, so a browser clock ahead of the server's is not refused", () => {
    const policy = { maxLifetimeSeconds: 365 * 24 * 60 * 60, expiryRequired: false };
    expect(expiresAt("365", now, policy)).toBe(
      new Date(now.getTime() + 365 * day - 60 * 60 * 1000).toISOString(),
    );
    const short = { maxLifetimeSeconds: 2 * 24 * 60 * 60, expiryRequired: true };
    expect(new Date(expiresAt("7", now, short)!).getTime()).toBeLessThan(now.getTime() + 2 * day);
  });
});

describe("a personal access token's state", () => {
  const token: ApiToken = {
    id: "t",
    name: "script",
    hint: "fn_pat_Ab3d",
    scopes: ["read"],
    expires_at: null,
    created_at: now.toISOString(),
    last_used_at: null,
    last_used_ip: null,
    revoked_at: null,
  };

  it("is active, expired once its expiry passed, and revoked above all", () => {
    expect(tokenState(token, now)).toBe("active");
    expect(tokenState({ ...token, expires_at: "2026-10-21T11:00:00Z" }, now)).toBe("expired");
    expect(
      tokenState(
        { ...token, expires_at: "2026-10-21T11:00:00Z", revoked_at: now.toISOString() },
        now,
      ),
    ).toBe("revoked");
  });
});
