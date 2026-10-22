import type { SegmentListItem } from "@flagnetic/api";
import { i18n } from "@lingui/core";
import { beforeAll, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import { newAttributeCondition } from "./conditions";
import {
  filterSegments,
  placeSegmentRefusal,
  segmentCreateOf,
  segmentDraft,
  segmentIssues,
  segmentPatchOf,
  validateSegmentsSearch,
} from "./segments";

beforeAll(() => activateForTests("en"));

const types = new Map([
  ["country", "string"],
  ["app_version", "semver"],
  ["user", "string"],
] as const);

const inEurope = { kind: "attribute", attribute: "country", op: "in", value: ["DE", "FR"] };

function listed(key: string, name: string, over: Partial<SegmentListItem> = {}): SegmentListItem {
  return {
    id: `id-${key}`,
    key,
    name,
    description: "",
    private: false,
    conditions: [],
    created_at: "2026-10-01T10:00:00Z",
    updated_at: "2026-10-01T10:00:00Z",
    version: 1,
    usage_count: 0,
    ...over,
  };
}

const segments = [
  listed("beta_testers", "Beta testers"),
  listed("eu_countries", "EU countries", { private: true }),
  listed("employees", "Staff"),
];

describe("the segments list's filters", () => {
  it("reads the address, dropping what it cannot read", () => {
    expect(validateSegmentsSearch({ q: "  eu ", private: "true" })).toEqual({
      q: "eu",
      private: true,
    });
    expect(validateSegmentsSearch({ private: false })).toEqual({ private: false });
    expect(validateSegmentsSearch({ q: " ", private: "maybe" })).toEqual({});
  });

  it("matches the text in the key or the name, whatever its case", () => {
    expect(filterSegments(segments, { q: "STAFF" }).map((s) => s.key)).toEqual(["employees"]);
    expect(filterSegments(segments, { q: "eu_" }).map((s) => s.key)).toEqual(["eu_countries"]);
  });

  it("keeps private segments only, or the others only", () => {
    expect(filterSegments(segments, { private: true }).map((s) => s.key)).toEqual(["eu_countries"]);
    expect(filterSegments(segments, { private: false }).map((s) => s.key)).toEqual([
      "beta_testers",
      "employees",
    ]);
  });
});

describe("a segment's draft", () => {
  const saved = listed("eu_countries", "EU countries", {
    description: "Where the EU rules apply.",
    conditions: [
      inEurope,
      { kind: "attribute", attribute: "app_version", op: "gte", value: "2.0.0" },
    ],
  });

  it("sends nothing while it holds what the segment has", () => {
    expect(segmentPatchOf(saved, segmentDraft(saved, types), types)).toEqual({});
  });

  it("sends only the fields changed, the conditions as a whole list", () => {
    const draft = segmentDraft(saved, types);
    expect(segmentPatchOf(saved, { ...draft, name: "Europe", private: true }, types)).toEqual({
      name: "Europe",
      private: true,
    });
    const fewer = { ...draft, conditions: draft.conditions.slice(0, 1) };
    expect(segmentPatchOf(saved, fewer, types)).toEqual({ conditions: [inEurope] });
  });

  it("finds what a condition still needs, except in a condition the segment already has", () => {
    const shorthand = listed("old", "Old", {
      conditions: [{ kind: "attribute", attribute: "app_version", op: "gte", value: "2.0" }],
    });
    const draft = segmentDraft(shorthand, types);
    expect(segmentIssues(i18n, shorthand, draft, types).size).toBe(0);

    const added = { ...newAttributeCondition("app_version", "semver"), op: "gte", text: "2.1" };
    const empty = newAttributeCondition("country", "string");
    const issues = segmentIssues(
      i18n,
      shorthand,
      { ...draft, conditions: [...draft.conditions, added, empty] },
      types,
    );
    expect([...issues.keys()]).toEqual([added.key, empty.key]);
    expect(issues.get(empty.key)).toBe("Enter a value.");
  });

  it("pins a refusal on the field or condition its pointer names, the rest apart", () => {
    const draft = segmentDraft(saved, types);
    const placed = placeSegmentRefusal(
      [
        { pointer: "/name", code: "too_long", detail: "" },
        { pointer: "/conditions/1/value", code: "invalid_value", detail: "" },
        { pointer: "/conditions/0", code: "unknown_attribute", detail: "" },
        { pointer: "/conditions", code: "too_many", detail: "", params: { max: 20 } },
      ],
      draft,
    );
    expect([...placed.at.keys()]).toEqual([
      "name",
      draft.conditions[1]!.key,
      draft.conditions[0]!.key,
    ]);
    expect(placed.rest.map((violation) => violation.pointer)).toEqual(["/conditions"]);
  });

  it("creates a segment with its key and what the draft holds, a blank description left out", () => {
    const draft = {
      name: "Employees",
      description: "  ",
      private: false,
      conditions: [{ ...newAttributeCondition("user", "string"), text: "qa-1" }],
    };
    expect(segmentCreateOf(" employees ", draft, types)).toEqual({
      key: "employees",
      name: "Employees",
      private: false,
      conditions: [{ kind: "attribute", attribute: "user", op: "eq", value: "qa-1" }],
    });
  });
});
