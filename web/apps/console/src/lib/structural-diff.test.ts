import type { FlagConfiguration, FlagDefinition, Rule, Segment } from "@flagnetic/api";
import { describe, expect, it } from "vitest";

import { diffConfigurations, diffDefinitions, diffSegments } from "./structural-diff";

function configuration(over: Partial<FlagConfiguration> = {}): FlagConfiguration {
  return {
    environment: "production",
    on: false,
    off_variation: "off",
    fallthrough: "on",
    prerequisites: [],
    overrides: [],
    rules: [],
    updated_at: "2026-10-01T10:00:00Z",
    updated_by: null,
    version: 1,
    ...over,
  };
}

function rule(id: string, over: Partial<Rule> = {}): Rule {
  return {
    id,
    description: id,
    conditions: [],
    serve: { kind: "variation", variation: "on" },
    ...over,
  };
}

const country = (value: string) => ({
  kind: "attribute",
  attribute: "country",
  op: "eq",
  value,
});

describe("diffConfigurations", () => {
  it("finds nothing between equal configurations, whatever their versions and authors", () => {
    expect(
      diffConfigurations(
        configuration({ rules: [rule("r1")] }),
        configuration({ rules: [rule("r1")], version: 4, updated_by: "m-2" }),
      ),
    ).toEqual([]);
  });

  it("reports on, the off variation and the fallthrough", () => {
    expect(
      diffConfigurations(
        configuration(),
        configuration({ on: true, off_variation: "control", fallthrough: "treatment" }),
      ),
    ).toEqual([
      { kind: "on", before: false, after: true },
      { kind: "off_variation", before: "off", after: "control" },
      { kind: "fallthrough", before: "on", after: "treatment" },
    ]);
  });

  it("reports prerequisites added, removed and requiring another variation", () => {
    expect(
      diffConfigurations(
        configuration({
          prerequisites: [
            { flag: "payments", variation: "on" },
            { flag: "search", variation: "on" },
          ],
        }),
        configuration({
          prerequisites: [
            { flag: "payments", variation: "off" },
            { flag: "billing", variation: "on" },
          ],
        }),
      ),
    ).toEqual([
      { kind: "prerequisite", change: "changed", flag: "payments", before: "on", after: "off" },
      { kind: "prerequisite", change: "removed", flag: "search", before: "on" },
      { kind: "prerequisite", change: "added", flag: "billing", after: "on" },
    ]);
  });

  it("reports overrides by unit, ignoring who wrote them and an absent note against an empty one", () => {
    expect(
      diffConfigurations(
        configuration({
          overrides: [
            { unit_type: "user", unit_id: "u-1", variation: "on", note: null, added_by: "m-1" },
            { unit_type: "user", unit_id: "u-2", variation: "on", note: "qa", added_by: "m-1" },
            { unit_type: "user", unit_id: "u-3", variation: "on", note: null, added_by: "m-1" },
          ],
        }),
        configuration({
          overrides: [
            { unit_type: "user", unit_id: "u-1", variation: "on", note: "", added_by: "m-2" },
            { unit_type: "user", unit_id: "u-2", variation: "off", note: "qa", added_by: "m-2" },
            { unit_type: "anonymous", unit_id: "u-3", variation: "on", note: null, added_by: null },
          ],
        }),
      ),
    ).toEqual([
      {
        kind: "override",
        change: "changed",
        unitType: "user",
        unitId: "u-2",
        before: { variation: "on", note: "qa" },
        after: { variation: "off", note: "qa" },
      },
      {
        kind: "override",
        change: "removed",
        unitType: "user",
        unitId: "u-3",
        before: { variation: "on", note: "" },
      },
      {
        kind: "override",
        change: "added",
        unitType: "anonymous",
        unitId: "u-3",
        after: { variation: "on", note: "" },
      },
    ]);
  });

  it("reports a rule added at its position and a rule removed from its old one", () => {
    expect(
      diffConfigurations(
        configuration({ rules: [rule("r1"), rule("r2")] }),
        configuration({ rules: [rule("r1"), rule("r3")] }),
      ),
    ).toEqual([
      { kind: "rule", change: "removed", position: 2, rule: rule("r2") },
      { kind: "rule", change: "added", position: 2, rule: rule("r3") },
    ]);
  });

  it("marks only the rule that moved, not the rules it pushed along", () => {
    expect(
      diffConfigurations(
        configuration({ rules: [rule("r1"), rule("r2"), rule("r3")] }),
        configuration({ rules: [rule("r3"), rule("r1"), rule("r2")] }),
      ),
    ).toEqual([{ kind: "rule", change: "changed", position: 1, movedFrom: 3, details: [] }]);
  });

  it("reports a rule's new description and what it serves", () => {
    expect(
      diffConfigurations(
        configuration({ rules: [rule("r1")] }),
        configuration({
          rules: [
            rule("r1", {
              description: "Germany",
              serve: {
                kind: "rollout",
                variation: "on",
                unit_type: "user",
                percentage: 2500,
                salt: "abcdefgh12345678",
              },
            }),
          ],
        }),
      ),
    ).toEqual([
      {
        kind: "rule",
        change: "changed",
        position: 1,
        details: [
          { kind: "description", before: "r1", after: "Germany" },
          {
            kind: "serve",
            before: { kind: "variation", variation: "on" },
            after: {
              kind: "rollout",
              variation: "on",
              unit_type: "user",
              percentage: 2500,
              salt: "abcdefgh12345678",
            },
          },
        ],
      },
    ]);
  });

  it("ignores a salt one side does not carry yet, and reports a regenerated one", () => {
    const rollout = (salt?: string) =>
      rule("r1", {
        serve: {
          kind: "rollout",
          variation: "on",
          unit_type: "user",
          percentage: 1000,
          ...(salt ? { salt } : {}),
        },
      });
    expect(
      diffConfigurations(
        configuration({ rules: [rollout()] }),
        configuration({ rules: [rollout("abcdefgh12345678")] }),
      ),
    ).toEqual([]);
    expect(
      diffConfigurations(
        configuration({ rules: [rollout("abcdefgh12345678")] }),
        configuration({ rules: [rollout("zyxwvuts87654321")] }),
      ),
    ).toHaveLength(1);
  });

  it("aligns conditions, so one inserted in front changes nothing else", () => {
    expect(
      diffConfigurations(
        configuration({ rules: [rule("r1", { conditions: [country("DE"), country("FR")] })] }),
        configuration({
          rules: [rule("r1", { conditions: [country("NL"), country("DE"), country("FR")] })],
        }),
      ),
    ).toEqual([
      {
        kind: "rule",
        change: "changed",
        position: 1,
        details: [{ kind: "condition", change: "added", position: 1, after: country("NL") }],
      },
    ]);
  });

  it("reports a condition changed in place and one removed", () => {
    expect(
      diffConfigurations(
        configuration({
          rules: [rule("r1", { conditions: [country("DE"), country("FR"), country("IT")] })],
        }),
        configuration({ rules: [rule("r1", { conditions: [country("AT"), country("FR")] })] }),
      ),
    ).toEqual([
      {
        kind: "rule",
        change: "changed",
        position: 1,
        details: [
          {
            kind: "condition",
            change: "changed",
            position: 1,
            before: country("DE"),
            after: country("AT"),
          },
          { kind: "condition", change: "removed", position: 3, before: country("IT") },
        ],
      },
    ]);
  });

  it("reports a rule both moved and changed once", () => {
    expect(
      diffConfigurations(
        configuration({ rules: [rule("r1"), rule("r2")] }),
        configuration({ rules: [rule("r2", { description: "first" }), rule("r1")] }),
      ),
    ).toEqual([
      {
        kind: "rule",
        change: "changed",
        position: 1,
        movedFrom: 2,
        details: [{ kind: "description", before: "r2", after: "first" }],
      },
    ]);
  });
});

function definition(over: Partial<FlagDefinition> = {}): FlagDefinition {
  return {
    id: "f-1",
    key: "checkout",
    type: "string",
    name: "Checkout",
    description: "",
    owner: "m-1",
    tags: ["web"],
    lifetime: "temporary",
    expected_removal_at: "2027-01-01T00:00:00Z",
    automatic_exposure: false,
    variations: [
      { key: "control", value: "a", name: "control", description: "" },
      { key: "treatment", value: "b", name: "treatment", description: "" },
    ],
    archived_at: null,
    created_by: "m-1",
    created_at: "2026-10-01T10:00:00Z",
    updated_at: "2026-10-01T10:00:00Z",
    version: 1,
    ...over,
  };
}

describe("diffDefinitions", () => {
  it("finds nothing between equal definitions, whatever their versions", () => {
    expect(
      diffDefinitions(definition(), definition({ version: 3, updated_at: "2026-10-02T00:00:00Z" })),
    ).toEqual([]);
  });

  it("reports the descriptive fields that changed", () => {
    expect(
      diffDefinitions(
        definition(),
        definition({
          name: "New checkout",
          owner: "m-2",
          lifetime: "permanent",
          expected_removal_at: null,
          automatic_exposure: true,
          archived_at: "2026-10-05T00:00:00Z",
        }),
      ),
    ).toEqual([
      { kind: "field", field: "name", before: "Checkout", after: "New checkout" },
      { kind: "field", field: "owner", before: "m-1", after: "m-2" },
      { kind: "field", field: "lifetime", before: "temporary", after: "permanent" },
      {
        kind: "field",
        field: "expected_removal_at",
        before: "2027-01-01T00:00:00Z",
        after: null,
      },
      { kind: "field", field: "automatic_exposure", before: false, after: true },
      { kind: "field", field: "archived", before: false, after: true },
    ]);
  });

  it("reports tags added and removed", () => {
    expect(diffDefinitions(definition(), definition({ tags: ["checkout"] }))).toEqual([
      { kind: "tag", change: "removed", tag: "web" },
      { kind: "tag", change: "added", tag: "checkout" },
    ]);
  });

  it("reports variations by key: added, removed, changed and reordered", () => {
    const [control, treatment] = definition().variations as [
      FlagDefinition["variations"][0],
      FlagDefinition["variations"][0],
    ];
    const extra = { key: "extra", value: "c", name: "extra", description: "" };
    expect(
      diffDefinitions(
        definition(),
        definition({ variations: [{ ...treatment, value: "B" }, extra] }),
      ),
    ).toEqual([
      { kind: "variation", change: "removed", key: "control", before: control },
      {
        kind: "variation",
        change: "changed",
        key: "treatment",
        before: treatment,
        after: { ...treatment, value: "B" },
      },
      { kind: "variation", change: "added", key: "extra", after: extra },
    ]);
    expect(diffDefinitions(definition(), definition({ variations: [treatment, control] }))).toEqual(
      [
        {
          kind: "variation_order",
          before: ["control", "treatment"],
          after: ["treatment", "control"],
        },
      ],
    );
  });
});

function segment(over: Partial<Segment> = {}): Segment {
  return {
    id: "s-1",
    key: "employees",
    name: "Employees",
    description: "",
    private: false,
    conditions: [],
    created_at: "2026-10-01T10:00:00Z",
    updated_at: "2026-10-01T10:00:00Z",
    version: 1,
    ...over,
  };
}

describe("diffSegments", () => {
  it("finds nothing between equal segments, whatever their versions and times", () => {
    expect(
      diffSegments(
        segment({ conditions: [country("NL")] }),
        segment({ conditions: [country("NL")], version: 3, updated_at: "2026-10-02T10:00:00Z" }),
      ),
    ).toEqual([]);
  });

  it("reports the name, the description and the private switch", () => {
    expect(
      diffSegments(segment(), segment({ name: "Staff", description: "Ours", private: true })),
    ).toEqual([
      { kind: "field", field: "name", before: "Employees", after: "Staff" },
      { kind: "field", field: "description", before: "", after: "Ours" },
      { kind: "field", field: "private", before: false, after: true },
    ]);
  });

  it("aligns the conditions, reporting those added, removed and changed by place", () => {
    const plan = { kind: "attribute", attribute: "plan", op: "eq", value: "pro" };
    expect(
      diffSegments(
        segment({ conditions: [country("NL"), plan] }),
        segment({ conditions: [country("DE"), plan, country("FR")] }),
      ),
    ).toEqual([
      {
        kind: "condition",
        change: "changed",
        position: 1,
        before: country("NL"),
        after: country("DE"),
      },
      { kind: "condition", change: "added", position: 3, after: country("FR") },
    ]);
    expect(
      diffSegments(segment({ conditions: [plan, country("NL")] }), segment({ conditions: [plan] })),
    ).toEqual([{ kind: "condition", change: "removed", position: 2, before: country("NL") }]);
  });
});
