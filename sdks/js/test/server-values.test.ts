// The values document flagnetic/server produces for a context while it
// renders a page: what remote evaluation answers a client key, masked, so
// flagnetic/client's `bootstrap` reads it without knowing where it came from
// and the page never tells how a value was reached. No network: these run in
// the browser environment too.
import { describe, expect, test } from "vitest";
import { createServerWith } from "../src/server/client.js";
import { createServer } from "../src/server.js";
import { ruleset, type RulesetOptions } from "./support/flags-fixture.js";
import { VirtualClock } from "./support/virtual-clock.js";

const germanUser = { units: { user: "u-1" }, attributes: { country: "DE" } };
const otherUser = { units: { user: "u-2" }, attributes: { country: "FR" } };

function offline(extra: RulesetOptions = {}) {
  return createServer("", "", { offline: ruleset(extra), logger: () => undefined });
}

describe("the values document for server rendering", () => {
  test("is the document a client key is answered: version, environment and every flag, masked", () => {
    const doc = offline().values(germanUser);

    expect(doc).toEqual({
      schema_version: 1,
      ruleset_version: 7,
      environment: { key: "production", protected: false },
      flags: {
        checkout: {
          type: "boolean",
          value: true,
          variation: "on",
          reason: "targeting_match",
          automatic_exposure: false,
          override_influenced: false,
        },
        paywall_copy: {
          type: "string",
          value: "Try free",
          variation: "short",
          reason: "targeting_match",
          automatic_exposure: false,
          override_influenced: false,
        },
        theme: {
          type: "json",
          value: { mode: "dark" },
          variation: "dark",
          reason: "targeting_match",
          automatic_exposure: true,
          override_influenced: false,
        },
      },
      ignored_attributes: [],
    });
  });

  test("names no rule, whatever served the value", () => {
    const json = JSON.stringify(offline().values(germanUser));
    expect(json).not.toContain("rule_id");
    expect(json).not.toContain("r-de");
    expect(json).not.toMatch(/"reason":"(rule|rollout|fallthrough|prerequisite_failed)"/);
  });

  test("evaluates for the context passed, private segments included", () => {
    const flags = offline();
    expect(flags.values(germanUser)?.flags.checkout?.value).toBe(true);
    expect(flags.values(otherUser)?.flags.checkout?.value).toBe(false);
  });

  test("carries the flags asked for and omits the keys the ruleset does not hold", () => {
    const doc = offline().values(germanUser, { flags: ["checkout", "no_such_flag"] });
    expect(Object.keys(doc?.flags ?? {})).toEqual(["checkout"]);
  });

  test("reports a protected environment, so the browser refuses local overrides there", () => {
    expect(offline({ protected: true }).values(germanUser)?.environment).toEqual({ key: "production", protected: true });
  });

  test("carries the value a private segment decided, as remote evaluation does for a client key, and never the segment", () => {
    const served = offline().values(germanUser)?.flags.checkout;
    expect(served).toMatchObject({ value: true, reason: "targeting_match" });
    expect(JSON.stringify(offline().values(germanUser))).not.toContain("germans");
  });

  test("is nothing at stage built_in: there is nothing to render from", async () => {
    const clock = new VirtualClock();
    const flags = createServerWith(
      "fn_srv_k",
      "http://platform.test",
      { stream: false, logger: () => undefined },
      { clock, fetch: () => new Promise<Response>(() => undefined) },
    );
    expect(flags.stage).toBe("built_in");
    expect(flags.values(germanUser)).toBeUndefined();
    await flags.close();
  });
});

describe("what rendering a page counts", () => {
  test("is the true reason, not the masked one, so a flag served only from a page is not stale", async () => {
    const { testServer } = await import("../src/server.js");
    const flags = testServer(ruleset(), { logger: () => undefined });
    flags.values(germanUser);
    expect(flags.recorder.summary()).toEqual(
      expect.arrayContaining([
        { flag: "checkout", variation: "on", reason: "rule", count: 1 },
        { flag: "paywall_copy", variation: "short", reason: "fallthrough", count: 1 },
      ]),
    );
  });
});
