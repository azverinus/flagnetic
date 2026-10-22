import type { Rule } from "@flagnetic/api";
import { i18n } from "@lingui/core";
import { beforeAll, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import type { AttributeTypes } from "./conditions";

import {
  basisPoints,
  maxRules,
  moveRule,
  newRule,
  percentText,
  type RuleDraft,
  ruleDrafts,
  ruleInputs,
  ruleIssues,
  withRule,
} from "./rule-editor";

beforeAll(() => activateForTests("en"));

const types = new Map([
  ["country", "string"],
  ["user", "string"],
] as const);

const saved: Rule[] = [
  {
    id: "r1",
    description: "Germany",
    conditions: [{ kind: "attribute", attribute: "country", op: "eq", value: "DE" }],
    serve: { kind: "variation", variation: "treatment" },
  },
  {
    id: "r2",
    description: "VIP rollout",
    conditions: [{ kind: "in_segment", segment: "vip" }],
    serve: {
      kind: "rollout",
      variation: "treatment",
      unit_type: "user",
      percentage: 2550,
      salt: "abcdefgh12345678",
    },
  },
];

describe("rules to and from the API", () => {
  it("sends saved rules back as they were, identifiers and salts included", () => {
    expect(ruleInputs(ruleDrafts(saved, types), types)).toEqual(saved);
  });

  it("shows a rollout's basis points as a percentage", () => {
    expect(ruleDrafts(saved, types)[1]!.serve).toMatchObject({
      kind: "rollout",
      percent: "25.5",
      unitType: "user",
      salt: "abcdefgh12345678",
    });
  });

  it("sends a new rule without an identifier and a new rollout without a salt", () => {
    const rule = newRule("control", "user");
    const rollout = { ...rule, serve: { ...rule.serve, kind: "rollout" as const, percent: "10" } };
    expect(ruleInputs([rollout], types)).toEqual([
      {
        description: "",
        conditions: [],
        serve: { kind: "rollout", variation: "control", unit_type: "user", percentage: 1000 },
      },
    ]);
  });

  it("keeps a saved salt while the rule serves a variation, for when it is a rollout again", () => {
    const [, rollout] = ruleDrafts(saved, types) as [RuleDraft, RuleDraft];
    const asVariation = { ...rollout, serve: { ...rollout.serve, kind: "variation" as const } };
    expect(ruleInputs([asVariation], types)[0]!.serve).toEqual({
      kind: "variation",
      variation: "treatment",
    });
    const back = { ...asVariation, serve: { ...asVariation.serve, kind: "rollout" as const } };
    expect(ruleInputs([back], types)[0]!.serve.salt).toBe("abcdefgh12345678");
  });
});

describe("a rollout's percentage", () => {
  it("is entered to a hundredth of a percent, as basis points", () => {
    expect(basisPoints("25.5")).toBe(2550);
    expect(basisPoints("0")).toBe(0);
    expect(basisPoints("100")).toBe(10000);
    expect(basisPoints("0.01")).toBe(1);
    expect(basisPoints(" 7 ")).toBe(700);
  });

  it("refuses what is no percentage of units", () => {
    for (const text of ["", "-1", "100.01", "1.234", "abc", "1e2", "50%"]) {
      expect(basisPoints(text), text).toBeUndefined();
    }
  });

  it("reads back without trailing zeros", () => {
    expect(percentText(2550)).toBe("25.5");
    expect(percentText(10000)).toBe("100");
    expect(percentText(1)).toBe("0.01");
  });
});

describe("adding and moving rules", () => {
  it("adds a rule matching every context and serving the variation given", () => {
    expect(newRule("control", "user")).toMatchObject({
      description: "",
      conditions: [],
      serve: { kind: "variation", variation: "control", unitType: "user", percent: "" },
    });
    expect(newRule("control", "user").id).toBeUndefined();
  });

  it("allows 50 rules", () => {
    expect(maxRules).toBe(50);
  });

  it("moves a rule and announces where it went, counting from 1", () => {
    const rules = ruleDrafts(saved, types);
    const moved = moveRule(rules, 1, 0);
    expect(moved.rules.map((rule) => rule.id)).toEqual(["r2", "r1"]);
    expect(moved.announcement).toEqual({ from: 2, to: 1 });
  });

  it("ignores a move past either end", () => {
    const rules = ruleDrafts(saved, types);
    expect(moveRule(rules, 0, -1).rules).toBe(rules);
    expect(moveRule(rules, 1, 2).rules).toBe(rules);
    expect(moveRule(rules, 1, 2).announcement).toBeUndefined();
  });

  it("replaces one rule by its key, leaving the others as they are", () => {
    const rules = ruleDrafts(saved, types);
    const edited = withRule(rules, rules[0]!.key, (rule) => ({ ...rule, description: "DE" }));
    expect(edited[0]!.description).toBe("DE");
    expect(edited[1]).toBe(rules[1]);
  });
});

describe("what a rule still needs", () => {
  const context = {
    types,
    segments: new Set(["vip"]),
    flags: new Map<string, string[]>(),
    self: "checkout",
    variations: ["control", "treatment"],
    unitTypes: new Set(["user"]),
  };

  it("passes the saved rules", () => {
    const issues = ruleIssues(i18n, ruleDrafts(saved, types), context);
    expect(issues.size).toBe(0);
  });

  it("names the condition and the serve that need fixing, by their keys", () => {
    const [first, second] = ruleDrafts(saved, types) as [RuleDraft, RuleDraft];
    const broken = [
      { ...first, conditions: [{ ...first.conditions[0]!, text: "" }] },
      { ...second, serve: { ...second.serve, percent: "101", unitType: "" } },
    ];
    const issues = ruleIssues(i18n, broken, context);
    expect(issues.get(broken[0]!.conditions[0]!.key)).toBe("Enter a value.");
    expect(issues.get(`${broken[1]!.key}/percent`)).toBe(
      "Enter a percentage from 0 to 100, to two decimal places.",
    );
    expect(issues.get(`${broken[1]!.key}/unit_type`)).toBe("Choose a unit type.");
  });

  it("does not check again a condition the saved configuration has", () => {
    const shorthand: Rule = {
      id: "r9",
      description: "",
      conditions: [{ kind: "attribute", attribute: "app_version", op: "gte", value: "2.0" }],
      serve: { kind: "variation", variation: "control" },
    };
    const versions: AttributeTypes = new Map([...types, ["app_version", "semver"] as const]);
    const withVersion = { ...context, types: versions };
    const [card] = ruleDrafts([shorthand], withVersion.types) as [RuleDraft];
    expect(ruleIssues(i18n, [card], withVersion).size).toBe(1);
    const saved = new Set([
      '{"attribute":"app_version","kind":"attribute","op":"gte","value":"2.0"}',
    ]);
    expect(ruleIssues(i18n, [card], { ...withVersion, saved }).size).toBe(0);
  });

  it("refuses a variation the flag no longer has", () => {
    const [first] = ruleDrafts(saved, types) as [RuleDraft];
    const gone = { ...first, serve: { ...first.serve, variation: "removed" } };
    expect(ruleIssues(i18n, [gone], context).get(`${gone.key}/variation`)).toBe(
      "The flag has no such variation.",
    );
  });
});
