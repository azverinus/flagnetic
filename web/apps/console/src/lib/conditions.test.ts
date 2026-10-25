import { i18n } from "@lingui/core";
import { beforeAll, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import {
  attributeOptions,
  type ConditionDraft,
  conditionDraft,
  conditionIssue,
  conditionOf,
  describeServe,
  isFullSemver,
  isRe2Pattern,
  localDateTime,
  newAttributeCondition,
  operatorsByType,
  rfc3339,
  valueShape,
  withAttribute,
  withOperator,
} from "./conditions";

beforeAll(() => activateForTests("en"));

const types = new Map([
  ["country", "string"],
  ["build_number", "number"],
  ["beta", "boolean"],
  ["app_version", "semver"],
  ["signed_up_at", "date"],
  ["groups", "list"],
  ["user", "string"],
] as const);

const attribute = (over: Partial<ConditionDraft> = {}): ConditionDraft => ({
  key: "c1",
  kind: "attribute",
  attribute: "country",
  op: "eq",
  text: "DE",
  list: [],
  segment: "",
  flag: "",
  variation: "",
  ...over,
});

const context = {
  types,
  segments: new Set(["vip"]),
  flags: new Map([
    ["payments", ["off", "on"]],
    ["search", ["control", "treatment"]],
  ]),
  self: "checkout",
};

describe("the operator table", () => {
  it("offers each attribute type the operators the server accepts for it", () => {
    expect(operatorsByType).toEqual({
      string: [
        "eq",
        "neq",
        "in",
        "not_in",
        "contains",
        "not_contains",
        "starts_with",
        "ends_with",
        "matches",
        "exists",
        "not_exists",
      ],
      number: ["eq", "neq", "lt", "lte", "gt", "gte", "in", "not_in", "exists", "not_exists"],
      boolean: ["eq", "exists", "not_exists"],
      semver: ["eq", "neq", "lt", "lte", "gt", "gte", "in", "not_in", "exists", "not_exists"],
      date: ["before", "after", "exists", "not_exists"],
      list: [
        "contains",
        "not_contains",
        "contains_any",
        "contains_all",
        "is_empty",
        "exists",
        "not_exists",
      ],
    });
  });

  it("gives an operator no value, one value or a list", () => {
    expect(valueShape("string", "exists")).toBe("none");
    expect(valueShape("list", "is_empty")).toBe("none");
    expect(valueShape("number", "not_exists")).toBe("none");
    expect(valueShape("string", "in")).toBe("list");
    expect(valueShape("semver", "not_in")).toBe("list");
    expect(valueShape("list", "contains_any")).toBe("list");
    expect(valueShape("list", "contains")).toBe("scalar");
    expect(valueShape("number", "gte")).toBe("scalar");
    expect(valueShape("date", "before")).toBe("scalar");
  });
});

describe("the attributes a condition can name", () => {
  it("lists declared attributes, then unit types as strings, each group by key", () => {
    expect(
      attributeOptions(
        [
          { key: "plan", type: "string", name: "Plan" },
          { key: "country", type: "string", name: "Country" },
        ],
        [{ key: "user", name: "User" }],
      ),
    ).toEqual([
      { key: "country", type: "string", name: "Country", unitType: false },
      { key: "plan", type: "string", name: "Plan", unitType: false },
      { key: "user", type: "string", name: "User", unitType: true },
    ]);
  });
});

describe("values", () => {
  it("sends each type's value as the server reads it", () => {
    expect(conditionOf(attribute(), types)).toEqual({
      kind: "attribute",
      attribute: "country",
      op: "eq",
      value: "DE",
    });
    expect(conditionOf(attribute({ attribute: "build_number", text: "42" }), types)).toMatchObject({
      value: 42,
    });
    expect(conditionOf(attribute({ attribute: "beta", text: "false" }), types)).toMatchObject({
      value: false,
    });
    expect(
      conditionOf(attribute({ attribute: "build_number", op: "in", list: ["1", "2.5"] }), types),
    ).toMatchObject({ value: [1, 2.5] });
    expect(
      conditionOf(attribute({ attribute: "app_version", op: "in", list: ["1.2.0"] }), types),
    ).toMatchObject({ value: ["1.2.0"] });
    expect(conditionOf(attribute({ op: "exists", text: "ignored" }), types)).toEqual({
      kind: "attribute",
      attribute: "country",
      op: "exists",
    });
  });

  it("reads a saved condition back to what it was", () => {
    const saved = [
      { kind: "attribute", attribute: "build_number", op: "in", value: [1, 2] },
      { kind: "attribute", attribute: "beta", op: "eq", value: true },
      { kind: "attribute", attribute: "signed_up_at", op: "after", value: "2026-01-01T00:00:00Z" },
      { kind: "attribute", attribute: "groups", op: "is_empty" },
      { kind: "in_segment", segment: "vip" },
      { kind: "prerequisite", flag: "payments", variation: "on" },
    ];
    for (const condition of saved) {
      expect(conditionOf(conditionDraft(condition, types), types)).toEqual(condition);
    }
  });

  it("turns a local date and time into an RFC 3339 instant and back", () => {
    const instant = rfc3339("2026-03-01T09:30");
    expect(instant).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/);
    expect(new Date(instant!).getTime()).toBe(new Date(2026, 2, 1, 9, 30).getTime());
    expect(localDateTime(instant!)).toBe("2026-03-01T09:30");
    expect(rfc3339("")).toBeUndefined();
  });

  it("takes a full semantic version only", () => {
    for (const version of ["1.41.0", "v2.0.0", "1.0.0-beta.1", "1.0.0+build.7", "10.20.30-rc-1"]) {
      expect(isFullSemver(version), version).toBe(true);
    }
    for (const version of ["2.0", "2", "1.2.3.4", "1.2.x", "", "1.0.0-", "1.0.0+"]) {
      expect(isFullSemver(version), version).toBe(false);
    }
  });

  it("takes a pattern RE2 runs, refusing look-around and back-references", () => {
    expect(isRe2Pattern("^de-[a-z]+$")).toBe(true);
    expect(isRe2Pattern("(?=x)")).toBe(false);
    expect(isRe2Pattern("(?<!x)y")).toBe(false);
    expect(isRe2Pattern("(a)\\1")).toBe(false);
    expect(isRe2Pattern("(")).toBe(false);
  });
});

describe("editing a condition", () => {
  it("starts an attribute condition on the first attribute's first operator, with no value", () => {
    expect(newAttributeCondition("country", "string")).toMatchObject({
      kind: "attribute",
      attribute: "country",
      op: "eq",
      text: "",
      list: [],
    });
    expect(newAttributeCondition("beta", "boolean")).toMatchObject({ op: "eq", text: "true" });
  });

  it("resets the operator and value when the attribute changes", () => {
    expect(withAttribute(attribute(), "signed_up_at", "date")).toMatchObject({
      attribute: "signed_up_at",
      op: "before",
      text: "",
    });
  });

  it("keeps the attribute and resets the value when the operator changes", () => {
    expect(withOperator(attribute({ list: ["DE"] }), "in", "string")).toMatchObject({
      attribute: "country",
      op: "in",
      text: "",
      list: [],
    });
  });
});

describe("what a condition still needs", () => {
  const issue = (draft: ConditionDraft) => conditionIssue(i18n, draft, context);

  it("passes a condition of every kind that is complete", () => {
    expect(issue(attribute())).toBeUndefined();
    expect(issue(attribute({ op: "exists", text: "" }))).toBeUndefined();
    expect(issue(attribute({ kind: "in_segment", segment: "vip" }))).toBeUndefined();
    expect(
      issue(attribute({ kind: "prerequisite", flag: "payments", variation: "on" })),
    ).toBeUndefined();
  });

  it("asks for an attribute the project declares and a value", () => {
    expect(issue(attribute({ attribute: "" }))).toBe("Choose an attribute.");
    expect(issue(attribute({ attribute: "plan" }))).toBe(
      "The project does not declare this attribute.",
    );
    expect(issue(attribute({ text: "" }))).toBe("Enter a value.");
    expect(issue(attribute({ op: "in", list: [] }))).toBe("Add at least one value.");
  });

  it("checks a value by the attribute's type", () => {
    expect(issue(attribute({ attribute: "build_number", text: "many" }))).toBe("Enter a number.");
    expect(issue(attribute({ attribute: "app_version", text: "2.0" }))).toBe(
      "Enter a full semantic version, such as 1.41.0.",
    );
    expect(issue(attribute({ attribute: "app_version", op: "in", list: ["1.0.0", "1.x"] }))).toBe(
      "1.x is not a full semantic version, such as 1.41.0.",
    );
    expect(issue(attribute({ attribute: "signed_up_at", op: "after", text: "" }))).toBe(
      "Enter a date and time.",
    );
    expect(issue(attribute({ op: "matches", text: "(?=x)" }))).toBe(
      "Enter a pattern in RE2 syntax, which has no look-around or back-references.",
    );
    expect(issue(attribute({ op: "neq", attribute: "beta" }))).toBe(
      "This operator does not apply to the attribute's type.",
    );
  });

  it("asks for a segment the project has", () => {
    expect(issue(attribute({ kind: "in_segment", segment: "" }))).toBe("Choose a segment.");
    expect(issue(attribute({ kind: "not_in_segment", segment: "gone" }))).toBe(
      "The project has no such segment.",
    );
  });

  it("asks for another flag of the project and one of its variations", () => {
    const prerequisite = (flag: string, variation: string) =>
      issue(attribute({ kind: "prerequisite", flag, variation }));
    expect(prerequisite("", "")).toBe("Choose a flag.");
    expect(prerequisite("checkout", "on")).toBe("A flag cannot require itself.");
    expect(prerequisite("gone", "on")).toBe("The project has no such flag, or it is archived.");
    expect(prerequisite("payments", "")).toBe("Choose a variation.");
    expect(prerequisite("payments", "treatment")).toBe("The flag has no such variation.");
  });
});

describe("what a rule serves in one line", () => {
  it("names a variation, a rollout and an experiment", () => {
    expect(describeServe(i18n, { kind: "variation", variation: "on" })).toBe("on");
    expect(
      describeServe(i18n, {
        kind: "rollout",
        variation: "on",
        unit_type: "user",
        percentage: 2500,
      }),
    ).toBe("on to 25% of user units");
    expect(describeServe(i18n, { kind: "experiment", experiment: "checkout-test" })).toBe(
      "experiment checkout-test",
    );
  });
});
