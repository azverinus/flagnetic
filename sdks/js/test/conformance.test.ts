// The conformance runner: every file under conformance/fixtures at the
// repository root, compared exactly, with no skip list. It runs in Node and
// in the browser project, so the fixtures are bundled through
// import.meta.glob instead of being read from disk.
import { describe, expect, test } from "vitest";
import { bucket, decodeRuleset, murmur3, RulesetDecodeError, type Context } from "../src/server.js";
import { oneCondition } from "./one-condition.js";

interface MurmurVectors {
  name: string;
  source: string;
  vectors: { input: string; hash: number; bucket: number }[];
}

interface RegexVectors {
  name: string;
  source: string;
  vectors: { pattern: string; input: string; match: boolean | "invalid" }[];
}

interface Fixture {
  name: string;
  ruleset: unknown;
  unsupported?: Record<string, { kind: string; name: string }[]>;
  cases: { name: string; context: Context; flag: string; expect: unknown }[];
}

const prefix = "../../../conformance/fixtures/";
const files = import.meta.glob<unknown>("../../../conformance/fixtures/**/*.json", {
  eager: true,
  import: "default",
});
const fixtures = Object.entries(files).map(([path, data]) => ({
  name: path.slice(prefix.length, -".json".length),
  data,
}));

test("the conformance suite is found", () => {
  const names = fixtures.map((f) => f.name);
  expect(names).toContain("murmur3");
  expect(names).toContain("regex");
  expect(names).toContain("unsupported/rules");
  expect(fixtures.length).toBeGreaterThan(2);
});

describe.each(fixtures)("$name", ({ name, data }) => {
  if (name === "murmur3") {
    const v = data as MurmurVectors;
    expectKeys(v, ["name", "source", "vectors"]);

    test("has vectors", () => {
      expect(v.vectors.length).toBeGreaterThan(0);
    });

    test.each(v.vectors)("$input", (vec) => {
      expectKeys(vec, ["input", "hash", "bucket"]);
      expect(murmur3(vec.input)).toBe(vec.hash);
      expect(murmur3(new TextEncoder().encode(vec.input))).toBe(vec.hash);
      expect(vec.hash % 10000).toBe(vec.bucket);
      const dot = vec.input.indexOf(".");
      if (dot >= 0) {
        expect(bucket(vec.input.slice(0, dot), vec.input.slice(dot + 1))).toBe(vec.bucket);
      }
    });
    return;
  }

  if (name === "regex") {
    const v = data as RegexVectors;
    expectKeys(v, ["name", "source", "vectors"]);

    test("has vectors", () => {
      expect(v.vectors.length).toBeGreaterThan(0);
    });

    // A vector is `true` or `false` for whether the pattern matches, or
    // "invalid" for a pattern outside RE2, which is an inconsistent ruleset.
    test.each(v.vectors.map((vec, i) => ({ ...vec, i })))("#$i $pattern on $input", (vec) => {
      expectKeys(vec, ["pattern", "input", "match", "i"]);
      const r = oneCondition("string", "matches", vec.pattern).evaluate("f", { attributes: { a: vec.input } });
      if (vec.match === "invalid") {
        expect(r.reason).toBe("error");
        expect(r.error_code).toBe("inconsistent_ruleset");
      } else {
        expect(r.reason).toBe(vec.match ? "rule" : "fallthrough");
      }
    });
    return;
  }

  const f = data as Fixture;
  expectKeys(f, ["name", "ruleset", "unsupported", "cases"]);

  test("its name is its path", () => {
    expect(f.name).toBe(name);
  });

  test("has cases", () => {
    expect(f.cases.length).toBeGreaterThan(0);
  });

  const rs = decodeRuleset(f.ruleset);

  test("reports the unsupported constructs the fixture expects", () => {
    expect(Object.fromEntries(rs.unsupportedRules())).toStrictEqual(f.unsupported ?? {});
  });

  test.each(f.cases)("$name", (c) => {
    expectKeys(c, ["name", "context", "flag", "expect"]);
    const got = rs.evaluate(c.flag, c.context);
    // Exact: every expected key with an equal value, and no other key.
    expect(got).toStrictEqual(c.expect);

    // Evaluate-all memoizes prerequisites within one call; it must agree
    // with evaluating the flag on its own.
    const all = rs.evaluateAll(c.context);
    if (all.has(c.flag)) {
      expect(all.get(c.flag)).toStrictEqual(got);
    }
  });
});

test("a scope other than server is refused", () => {
  expect(() => decodeRuleset({ schema_version: 1, version: 1, scope: "client" })).toThrow(RulesetDecodeError);
});

// expectKeys refuses a fixture with a key this runner does not know, as the
// Go runner's strict decoding does, so a new fixture field cannot be
// silently ignored.
function expectKeys(value: object, known: string[]): void {
  const unknown = Object.keys(value).filter((k) => !known.includes(k));
  if (unknown.length > 0) {
    throw new Error(`unknown fixture keys: ${unknown.join(", ")}`);
  }
}
