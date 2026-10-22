import type { FlagConfiguration, Rule } from "@flagnetic/api";
import { describe, expect, it } from "vitest";

import { comparisonRows, sameRuleset } from "./compare";

function configuration(over: Partial<FlagConfiguration> = {}): FlagConfiguration {
  return {
    environment: "staging",
    on: true,
    off_variation: "control",
    fallthrough: "control",
    prerequisites: [],
    overrides: [],
    rules: [],
    updated_at: "2026-10-01T10:00:00Z",
    updated_by: null,
    version: 3,
    ...over,
  };
}

function rollout(id: string, salt: string, over: Partial<Rule> = {}): Rule {
  return {
    id,
    description: "Half of Europe",
    conditions: [{ kind: "attribute", attribute: "country", op: "in", value: ["DE", "FR"] }],
    serve: { kind: "rollout", variation: "treatment", unit_type: "user", percentage: 5000, salt },
    ...over,
  };
}

describe("whether two configurations serve the same", () => {
  it("ignores rule descriptions, override notes and authors, versions and environments", () => {
    const staging = configuration({
      rules: [rollout("r1", "a1b2c3d4e5f6a7b8")],
      overrides: [
        {
          unit_type: "user",
          unit_id: "u-1",
          variation: "treatment",
          note: "QA account",
          added_by: "m-1",
        },
      ],
    });
    const production = configuration({
      environment: "production",
      version: 9,
      updated_at: "2026-10-05T10:00:00Z",
      rules: [rollout("r1", "a1b2c3d4e5f6a7b8", { description: "Europe at 50%" })],
      overrides: [
        { unit_type: "user", unit_id: "u-1", variation: "treatment", note: null, added_by: "m-2" },
      ],
    });

    expect(sameRuleset(staging, production)).toBe(true);
  });

  it("tells apart on and off, the off and fallthrough variations", () => {
    expect(sameRuleset(configuration(), configuration({ on: false }))).toBe(false);
    expect(sameRuleset(configuration(), configuration({ off_variation: "treatment" }))).toBe(false);
    expect(sameRuleset(configuration(), configuration({ fallthrough: "treatment" }))).toBe(false);
  });

  it("tells apart a rollout's salt, as units fall into it by the salt", () => {
    expect(
      sameRuleset(
        configuration({ rules: [rollout("r1", "a1b2c3d4e5f6a7b8")] }),
        configuration({ rules: [rollout("r1", "ffffffffffffffff")] }),
      ),
    ).toBe(false);
  });

  it("tells apart rules of the same conditions and serve with other identifiers", () => {
    expect(
      sameRuleset(
        configuration({ rules: [rollout("r1", "a1b2c3d4e5f6a7b8")] }),
        configuration({ rules: [rollout("r2", "a1b2c3d4e5f6a7b8")] }),
      ),
    ).toBe(false);
  });

  it("tells apart the same overrides or prerequisites in another order", () => {
    const pins = [
      { unit_type: "user", unit_id: "u-1", variation: "treatment", note: null, added_by: null },
      { unit_type: "user", unit_id: "u-2", variation: "control", note: null, added_by: null },
    ];
    expect(
      sameRuleset(
        configuration({ overrides: pins }),
        configuration({ overrides: [...pins].reverse() }),
      ),
    ).toBe(false);
    const needs = [
      { flag: "checkout", variation: "on" },
      { flag: "search", variation: "on" },
    ];
    expect(
      sameRuleset(
        configuration({ prerequisites: needs }),
        configuration({ prerequisites: [...needs].reverse() }),
      ),
    ).toBe(false);
  });

  it("reads a condition's members in any order as the same condition", () => {
    const written = configuration({
      rules: [
        rollout("r1", "a1b2c3d4e5f6a7b8", {
          conditions: [{ value: ["DE", "FR"], op: "in", attribute: "country", kind: "attribute" }],
        }),
      ],
    });
    expect(
      sameRuleset(written, configuration({ rules: [rollout("r1", "a1b2c3d4e5f6a7b8")] })),
    ).toBe(true);
  });
});

describe("the rows of a side-by-side comparison", () => {
  it("lays both configurations out in evaluation order, marking what differs", () => {
    const left = configuration({
      on: false,
      rules: [rollout("r1", "a1b2c3d4e5f6a7b8"), rollout("r2", "0000000000000000")],
    });
    const right = configuration({ rules: [rollout("r1", "a1b2c3d4e5f6a7b8")] });

    expect(comparisonRows(left, right).map((row) => [row.part, row.differs])).toEqual([
      ["on", true],
      ["off_variation", false],
      ["overrides", false],
      ["prerequisites", false],
      ["rule", false],
      ["rule", true],
      ["fallthrough", false],
    ]);
  });

  it("pairs rules by position, one side lacking a rule the other has", () => {
    const rows = comparisonRows(
      configuration(),
      configuration({ rules: [rollout("r1", "a1b2c3d4e5f6a7b8")] }),
    );
    const rule = rows.find((row) => row.part === "rule");
    expect(rule).toMatchObject({ position: 1, left: undefined, differs: true });
    expect(rule?.right).toMatchObject({ id: "r1" });
  });
});
