import { setupI18n } from "@lingui/core";
import { afterEach, describe, expect, it } from "vitest";

import type { RuleDraft } from "./rule-editor";
import {
  buildContext,
  contextDraftOf,
  contextLabel,
  matchedRuleOf,
  recentContexts,
  rememberContext,
} from "./tester";

const i18n = setupI18n({ locale: "en", messages: { en: {} } });

const types = new Map([
  ["user", "string"],
  ["platform", "string"],
  ["age", "number"],
  ["beta", "boolean"],
  ["groups", "list"],
] as const);

afterEach(() => {
  localStorage.clear();
});

describe("the tester's context", () => {
  it("is the units with an ID and the attributes chosen, each typed", () => {
    const built = buildContext(
      i18n,
      {
        units: [
          { unitType: "user", unitId: "emp-1" },
          { unitType: "anonymous", unitId: "" },
        ],
        attributes: [
          { attribute: "age", text: "42", list: [] },
          { attribute: "beta", text: "true", list: [] },
          { attribute: "groups", text: "", list: ["staff", "qa"] },
          { attribute: "", text: "ignored", list: [] },
        ],
      },
      types,
    );
    expect(built.issues.size).toBe(0);
    expect(built.context).toEqual({
      units: { user: "emp-1" },
      attributes: { age: 42, beta: true, groups: ["staff", "qa"] },
    });
  });

  it("refuses a unit type twice, a unit ID over 256 bytes and a value not of the attribute's type", () => {
    const built = buildContext(
      i18n,
      {
        units: [
          { unitType: "user", unitId: "a" },
          { unitType: "user", unitId: "b" },
          { unitType: "anonymous", unitId: "é".repeat(129) },
        ],
        attributes: [{ attribute: "age", text: "forty", list: [] }],
      },
      types,
    );
    expect([...built.issues.keys()]).toEqual(["unit-1", "unit-2", "attribute-0"]);
  });

  it("reads as its units and attributes in one line", () => {
    expect(
      contextLabel({
        units: { user: "emp-1" },
        attributes: { platform: "ios", groups: ["a", "b"] },
      }),
    ).toBe("user emp-1 · platform=ios · groups=a|b");
    expect(contextLabel({})).toBe("");
  });

  it("is laid out again from a context it built", () => {
    const context = { units: { user: "emp-1" }, attributes: { age: 42, groups: ["staff"] } };
    expect(buildContext(i18n, contextDraftOf(context, types), types).context).toEqual(context);
  });
});

describe("the contexts a project's tester remembers", () => {
  it("keeps the ten newest first, each once whatever the order of its members", () => {
    for (let i = 0; i < 12; i++) rememberContext("checkout", { units: { user: `u-${i}` } });
    rememberContext("checkout", { attributes: { platform: "ios", age: 3 } });
    rememberContext("checkout", { attributes: { age: 3, platform: "ios" } });

    const recent = recentContexts("checkout");
    expect(recent).toHaveLength(10);
    expect(recent[0]).toEqual({ attributes: { age: 3, platform: "ios" } });
    expect(recent[1]).toEqual({ units: { user: "u-11" } });
    expect(recentContexts("billing")).toEqual([]);
  });

  it("forgets what it cannot read rather than failing", () => {
    localStorage.setItem("flagnetic.recent-contexts.checkout", "{not json");
    expect(recentContexts("checkout")).toEqual([]);
    localStorage.setItem(
      "flagnetic.recent-contexts.checkout",
      JSON.stringify([3, { units: { user: "u" } }]),
    );
    expect(recentContexts("checkout")).toEqual([{ units: { user: "u" } }]);
  });
});

describe("the rule a result names", () => {
  const card = (key: string, id?: string): RuleDraft => ({
    key,
    ...(id ? { id } : {}),
    description: "",
    conditions: [],
    serve: { kind: "variation", variation: "on", experiment: "", unitType: "user", percent: "" },
  });

  it("is the card of the rule by its identifier, with its position", () => {
    expect(matchedRuleOf("r2", [card("r1", "r1"), card("r2", "r2")])).toEqual({
      key: "r2",
      position: 2,
    });
  });

  it("is the one new card of a draft, whose identifier the server made up", () => {
    expect(matchedRuleOf("tmp-9", [card("r1", "r1"), card("new-rule-1")])).toEqual({
      key: "new-rule-1",
      position: 2,
      unsaved: true,
    });
    expect(matchedRuleOf("tmp-9", [card("new-rule-1"), card("new-rule-2")])).toBeUndefined();
    expect(matchedRuleOf(undefined, [card("r1", "r1")])).toBeUndefined();
  });
});
