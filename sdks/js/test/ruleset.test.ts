// Mirrors eval/ruleset_test.go and eval/memo_internal_test.go: decoding,
// the operator table and memoization, observed through the public API.
import { describe, expect, test } from "vitest";
import { decodeRuleset, RulesetDecodeError, type Context } from "../src/server.js";
import { oneCondition } from "./one-condition.js";

describe("decodeRuleset refuses what it cannot read", () => {
  test.each([
    ["malformed JSON", `{"schema_version":1,`],
    ["missing schema_version", `{"version":1,"scope":"server"}`],
    ["future schema_version", `{"schema_version":2,"version":1,"scope":"server"}`],
    ["unknown scope", `{"schema_version":1,"version":1,"scope":"edge"}`],
    ["flags of the wrong JSON type", `{"schema_version":1,"version":1,"scope":"server","flags":{}}`],
    // Beyond the Go cases: every known field of the wrong JSON type is
    // refused as Go's encoding/json refuses it.
    ["a document that is not an object", `[]`],
    ["a non-integral version", `{"schema_version":1,"version":1.5,"scope":"server"}`],
    ["a string where a boolean belongs", `{"schema_version":1,"version":1,"scope":"server","flags":[{"key":"f","on":"yes"}]}`],
    ["a number where a string belongs", `{"schema_version":1,"version":1,"scope":"server","unit_types":[1]}`],
    ["a non-integral percentage", `{"schema_version":1,"version":1,"scope":"server","flags":[{"key":"f","rules":[{"id":"r","serve":{"kind":"rollout","percentage":1.5}}]}]}`],
    ["an object where weights belong", `{"schema_version":1,"version":1,"scope":"server","flags":[{"key":"f","rules":[{"id":"r","serve":{"kind":"experiment","weights":{}}}]}]}`],
    ["a byte order mark", `\ufeff{"schema_version":1,"version":1,"scope":"server"}`],
    ["a condition value beyond the double range", `{"schema_version":1,"version":1,"scope":"server","flags":[{"key":"f","rules":[{"id":"r","conditions":[{"kind":"attribute","attribute":"n","op":"gt","value":1e400}]}]}]}`],
    ["the last key matching a field wins", `{"schema_version":1,"version":1,"scope":"server","Scope":"edge"}`],
  ])("%s", (_, doc) => {
    expect(() => decodeRuleset(doc)).toThrow(RulesetDecodeError);
  });

  test("bytes with a byte order mark are refused, as encoding/json refuses them", () => {
    const bom = new Uint8Array([0xef, 0xbb, 0xbf, ...new TextEncoder().encode(`{"schema_version":1,"version":1,"scope":"server"}`)]);
    expect(() => decodeRuleset(bom)).toThrow(RulesetDecodeError);
  });

  test("keys match fields case-insensitively, as encoding/json matches them", () => {
    const rs = decodeRuleset(`{"SCHEMA_VERSION":1,"Version":3,"scope":"edge","\u017fcope":"server",
      "Flags":[{"KEY":"f","ON":false,"variations":[{"key":"a","Value":1}],"off_variation":"a"}]}`);
    expect(rs.version).toBe(3);
    expect(rs.scope).toBe("server");
    expect(rs.evaluate("f", {})).toStrictEqual({
      type: "",
      value: 1,
      variation: "a",
      reason: "off",
      automatic_exposure: false,
      override_influenced: false,
    });
  });

  test("bytes are read as UTF-8 JSON", () => {
    const rs = decodeRuleset(new TextEncoder().encode(`{"schema_version":1,"version":7,"scope":"server"}`));
    expect(rs.version).toBe(7);
    expect(rs.scope).toBe("server");
  });

  test("null is the zero value and unknown fields are ignored, as in Go", () => {
    const rs = decodeRuleset(`{"schema_version":1,"version":1,"scope":"server","unit_types":null,
      "environment":null,"future_field":{"x":1},"flags":[{"key":"f","type":"boolean","on":null,"shiny":true,
      "variations":[{"key":"false","value":false}],"off_variation":"false"}]}`);
    expect(rs.evaluate("f", {})).toStrictEqual({
      type: "boolean",
      value: false,
      variation: "false",
      reason: "off",
      automatic_exposure: false,
      override_influenced: false,
    });
  });
});

test("protected environment", () => {
  const cases: [string, boolean][] = [
    [`{"schema_version":1,"version":1,"scope":"server","environment":{"key":"production","protected":true}}`, true],
    [`{"schema_version":1,"version":1,"scope":"server","environment":{"key":"staging","protected":false}}`, false],
    [`{"schema_version":1,"version":1,"scope":"server"}`, false],
  ];
  for (const [doc, want] of cases) {
    expect(decodeRuleset(doc).protected, doc).toBe(want);
  }
});

test("flag looks a flag up by key", () => {
  const rs = decodeRuleset(`{"schema_version":1,"version":1,"scope":"server","flags":[
    {"key":"copy","type":"string","automatic_exposure":true,"on":false,"variations":[{"key":"a","value":"A"}],"off_variation":"a"}]}`);
  expect(rs.flag("copy")).toMatchObject({ key: "copy", type: "string", automatic_exposure: true });
  expect(rs.flag("nope")).toBeUndefined();
  // Keys are data, never object properties.
  expect(rs.flag("constructor")).toBeUndefined();
  expect(rs.evaluate("__proto__", {})).toStrictEqual({
    reason: "error",
    error_code: "flag_not_found",
    override_influenced: false,
  });
});

// Go's TestContextAcceptsGoValues: a JavaScript caller passes native values,
// not only what JSON.parse produces.
describe("context accepts JavaScript values", () => {
  const rs = decodeRuleset(`{"schema_version":1,"version":1,"scope":"server","unit_types":["user"],
    "attributes":[{"key":"n","type":"number"},{"key":"l","type":"list"},{"key":"d","type":"date"}],
    "flags":[{"key":"f","type":"string","on":true,"off_variation":"off","fallthrough":"off",
      "variations":[{"key":"off","value":"off"},{"key":"on","value":{"copy":"Try free"}}],
      "rules":[{"id":"r1","conditions":[
        {"kind":"attribute","attribute":"n","op":"gte","value":10},
        {"kind":"attribute","attribute":"l","op":"contains","value":"beta"},
        {"kind":"attribute","attribute":"d","op":"before","value":"2026-09-19T12:00:00Z"}],
       "serve":{"kind":"variation","variation":"on"}}]}]}`);
  const noon = Date.UTC(2026, 8, 19, 12);

  test.each<[string, Record<string, unknown>]>([
    ["number, frozen array, Date", { n: 10, l: Object.freeze(["beta"]), d: new Date(noon - 1000) }],
    ["number, array, epoch milliseconds", { n: 11, l: ["beta"], d: noon - 1 }],
    ["fraction, array, RFC 3339", { n: 12.5, l: ["alpha", "beta"], d: "2026-09-19T11:00:00Z" }],
  ])("%s", (_, attributes) => {
    const r = rs.evaluate("f", { attributes });
    expect(r).toStrictEqual({
      type: "string",
      value: { copy: "Try free" },
      variation: "on",
      reason: "rule",
      rule_id: "r1",
      automatic_exposure: false,
      override_influenced: false,
    });
  });

  test.each<[string, Record<string, unknown>]>([
    ["NaN is not a number", { n: Number.NaN, l: ["beta"], d: noon - 1 }],
    ["Infinity is not a number", { n: Number.POSITIVE_INFINITY, l: ["beta"], d: noon - 1 }],
    ["a numeric string is not a number", { n: "12", l: ["beta"], d: noon - 1 }],
    ["an invalid Date is not a date", { n: 12, l: ["beta"], d: new Date(Number.NaN) }],
    ["a list with a number is not a list", { n: 12, l: ["beta", 1], d: noon - 1 }],
  ])("%s", (_, attributes) => {
    expect(rs.evaluate("f", { attributes }).reason).toBe("fallthrough");
  });

  test("an inherited property is not an attribute", () => {
    const attributes = Object.create({ n: 10, l: ["beta"], d: noon - 1 }) as Record<string, unknown>;
    expect(rs.evaluate("f", { attributes }).reason).toBe("fallthrough");
  });
});

test("evaluateAll covers every flag", () => {
  const rs = decodeRuleset(`{"schema_version":1,"version":1,"scope":"server","unit_types":[],
    "flags":[{"key":"a","type":"boolean","on":false,"off_variation":"false",
      "variations":[{"key":"true","value":true},{"key":"false","value":false}]},
      {"key":"b","type":"boolean","on":false,"off_variation":"true",
      "variations":[{"key":"true","value":true},{"key":"false","value":false}]}]}`);
  const all = rs.evaluateAll({});
  expect(all.size).toBe(2);
  expect(all.get("a")?.reason).toBe("off");
  expect(all.get("b")?.variation).toBe("true");
});

// Go's TestCheckOperatorFollowsTheOperatorTable, through evaluation: a
// condition the evaluator cannot apply is inconsistent_ruleset; one it can
// apply evaluates.
describe("the operator table", () => {
  test.each<[string, string, unknown, boolean]>([
    ["string", "matches", "^a", true],
    ["string", "matches", "(?=a)", false],
    ["string", "lt", "a", false],
    ["string", "in", ["a", "b"], true],
    ["string", "in", ["a", 1.0], false],
    ["number", "gte", 1.5, true],
    ["number", "gte", "1.5", false],
    ["number", "in", [1, 2.5], true],
    ["number", "in", [1, "2"], false],
    ["boolean", "eq", false, true],
    ["boolean", "neq", false, false],
    ["semver", "lt", "1.41", true],
    ["semver", "lt", 1.41, false],
    ["date", "before", "2026-01-01T00:00:00Z", true],
    ["date", "before", 1767225600000, false],
    ["date", "eq", "2026-01-01T00:00:00Z", false],
    ["list", "contains_all", ["a"], true],
    ["list", "is_empty", null, true],
    ["string", "is_empty", null, false],
    ["string", "exists", null, true],
    ["object", "exists", null, false],
  ])("%s %s %j applies: %s", (type, op, value, ok) => {
    const r = oneCondition(type, op, value).evaluate("f", { attributes: {} });
    if (ok) {
      expect(r.reason).not.toBe("error");
    } else {
      expect(r).toStrictEqual({
        type: "boolean",
        reason: "error",
        error_code: "inconsistent_ruleset",
        automatic_exposure: false,
        override_influenced: false,
      });
    }
  });

  test("an unknown operator, condition kind or serve kind is inconsistent_ruleset", () => {
    const doc = (condition: string, serve: string) =>
      decodeRuleset(`{"schema_version":1,"version":1,"scope":"server","attributes":[{"key":"a","type":"string"}],
        "flags":[{"key":"f","type":"boolean","on":true,"off_variation":"false","fallthrough":"false",
          "variations":[{"key":"true","value":true},{"key":"false","value":false}],
          "rules":[{"id":"r1","conditions":[${condition}],"serve":${serve}}]}]}`);
    const variation = `{"kind":"variation","variation":"true"}`;
    const error = {
      type: "boolean",
      reason: "error",
      error_code: "inconsistent_ruleset",
      automatic_exposure: false,
      override_influenced: false,
    };
    expect(doc(`{"kind":"attribute","attribute":"a","op":"sounds_like","value":"x"}`, variation).evaluate("f", {})).toStrictEqual(error);
    expect(doc(`{"kind":"geo_fence"}`, variation).evaluate("f", {})).toStrictEqual(error);
    expect(doc(``, `{"kind":"multi_armed_bandit"}`).evaluate("f", {})).toStrictEqual(error);
  });
});

// Go's TestEvaluateAllMemoizesPrerequisites, observed from outside: a
// lattice of 20 levels of 6 flags, each requiring every flag of the level
// below, needs 6^20 computations without memoization and 120 with it.
test("prerequisites are memoized within one call", () => {
  const levels = 20;
  const width = 6;
  const flags: string[] = [];
  for (let l = 0; l < levels; l++) {
    for (let w = 0; w < width; w++) {
      const prereqs = l === 0 ? [] : Array.from({ length: width }, (_, p) => `{"flag":"f${l - 1}_${p}","variation":"true"}`);
      flags.push(`{"key":"f${l}_${w}","type":"boolean","on":true,
        "variations":[{"key":"true","value":true},{"key":"false","value":false}],
        "off_variation":"false","fallthrough":"true","prerequisites":[${prereqs.join(",")}]}`);
    }
  }
  const rs = decodeRuleset(`{"schema_version":1,"version":1,"scope":"server","unit_types":[],"flags":[${flags.join(",")}]}`);
  expect(rs.evaluate(`f${levels - 1}_0`, {}).reason).toBe("fallthrough");
  const all = rs.evaluateAll({});
  expect(all.size).toBe(levels * width);
  for (const r of all.values()) expect(r.reason).toBe("fallthrough");
}, 2000);

test("served values cannot be mutated through a result", () => {
  const rs = decodeRuleset(`{"schema_version":1,"version":1,"scope":"server","flags":[{"key":"f","type":"json","on":false,
    "variations":[{"key":"a","value":{"list":[1,2]}}],"off_variation":"a"}]}`);
  const value = rs.evaluate("f", {}).value as { list: number[] };
  expect(() => value.list.push(3)).toThrow(TypeError);
  expect(rs.evaluate("f", {}).value).toStrictEqual({ list: [1, 2] });
});

test("a context with no units skips units, and an empty unit ID is absent", () => {
  const rs = decodeRuleset(`{"schema_version":1,"version":1,"scope":"server","unit_types":["user"],"flags":[{"key":"f","type":"boolean","on":true,
    "variations":[{"key":"true","value":true},{"key":"false","value":false}],"off_variation":"false","fallthrough":"false",
    "overrides":[{"unit_type":"user","unit_id":"","variation":"true"}],
    "rules":[{"id":"r1","conditions":[{"kind":"attribute","attribute":"user","op":"not_exists"}],"serve":{"kind":"variation","variation":"true"}}]}]}`);
  const contexts: Context[] = [{}, { units: {} }, { units: { user: "" } }];
  for (const ctx of contexts) {
    expect(rs.evaluate("f", ctx)).toStrictEqual({
      type: "boolean",
      value: true,
      variation: "true",
      reason: "rule",
      rule_id: "r1",
      automatic_exposure: false,
      override_influenced: false,
    });
  }
});
