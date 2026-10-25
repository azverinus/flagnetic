// What both OpenFeature providers translate: a served detail into a
// resolution, an evaluation context into a Flagnetic context and a `track`
// call's details into an exposure or an alias. Plain functions over plain
// shapes, so this runs in Node and in the browser without either OpenFeature
// package loaded.
import { describe, expect, test } from "vitest";
import { reasonOf, REASON_METADATA_KEY } from "../src/openfeature.js";
import { toFlagneticContext } from "../src/openfeature/context.js";
import { resolution, type DetailLike } from "../src/openfeature/mapping.js";
import { trackedAlias, trackedFlag } from "../src/openfeature/tracking.js";

interface ReasonsFile {
  name: string;
  metadata_key: string;
  reasons: Record<string, string>;
}

const files = import.meta.glob<ReasonsFile>("../../../conformance/openfeature/reasons.json", {
  eager: true,
  import: "default",
});
const fixture = Object.values(files)[0];

function detail(over: Partial<DetailLike<unknown>>): DetailLike<unknown> {
  return { value: true, reason: "off", stage: "fresh", overridden: false, ...over };
}

describe("reasons, as the shared OpenFeature fixture maps them", () => {
  test("the fixture is found and names the metadata key", () => {
    expect(fixture?.name).toBe("openfeature/reasons");
    expect(REASON_METADATA_KEY).toBe(fixture?.metadata_key);
  });

  test.each(Object.entries(fixture?.reasons ?? {}))("%s maps to %s", (reason, mapped) => {
    expect(reasonOf(reason)).toBe(mapped);
  });

  test("a reason a newer server adds maps to UNKNOWN", () => {
    expect(reasonOf("brand_new")).toBe("UNKNOWN");
  });

  test.each(Object.entries(fixture?.reasons ?? {}).filter(([reason]) => reason !== "fallback"))(
    "a value served with reason %s resolves that reason and carries the Flagnetic one",
    (reason, mapped) => {
      const r = resolution(detail({ reason }));
      expect(r.reason).toBe(mapped);
      expect(r.flagMetadata[REASON_METADATA_KEY]).toBe(reason);
    },
  );

  test("targeting_match, what a client key is told, is TARGETING_MATCH", () => {
    expect(resolution(detail({ reason: "targeting_match", variation: "on" }))).toMatchObject({ reason: "TARGETING_MATCH", variant: "on" });
  });
});

describe("fallback", () => {
  test.each([
    ["flag_not_found", "FLAG_NOT_FOUND"],
    ["type_mismatch", "TYPE_MISMATCH"],
    ["no_ruleset", "GENERAL"],
    ["no_values", "GENERAL"],
    ["inconsistent_ruleset", "GENERAL"],
    ["prerequisite_cycle", "GENERAL"],
    ["general", "GENERAL"],
  ])("error code %s resolves ERROR with %s and the Flagnetic code as message", (code, mapped) => {
    const r = resolution(detail({ reason: "fallback", errorCode: code, value: "default" }));
    expect(r).toMatchObject({ value: "default", reason: "ERROR", errorCode: mapped, errorMessage: code });
    expect(r.variant).toBeUndefined();
    expect(r.flagMetadata[REASON_METADATA_KEY]).toBe("fallback");
  });

  test("a fallback without an error code is GENERAL", () => {
    expect(resolution(detail({ reason: "fallback" }))).toMatchObject({ reason: "ERROR", errorCode: "GENERAL" });
  });

  test("a served value carries no error code or message", () => {
    const r = resolution(detail({ reason: "rule", variation: "on" }));
    expect(r).not.toHaveProperty("errorCode");
    expect(r).not.toHaveProperty("errorMessage");
  });
});

describe("flag metadata", () => {
  test("reason, stage, ruleset version, variation and overridden, the variation also the variant", () => {
    const r = resolution(detail({ reason: "rule", variation: "on", rulesetVersion: 7, stage: "last_good", overridden: true }));
    expect(r.variant).toBe("on");
    expect(r.flagMetadata).toStrictEqual({
      flagnetic_reason: "rule",
      stage: "last_good",
      ruleset_version: 7,
      variation: "on",
      overridden: true,
    });
  });

  test("ruleset version and variation are left out when the detail has none", () => {
    expect(resolution(detail({ reason: "fallback", stage: "built_in" })).flagMetadata).toStrictEqual({
      flagnetic_reason: "fallback",
      stage: "built_in",
      overridden: false,
    });
  });

  test("an experiment block travels flattened, without its salts", () => {
    const experiment = {
      key: "checkout_v2",
      variation: "control",
      unitType: "anonymous",
      unitId: "u_10482",
      inclusionSalt: "a3f9c2e17b4d0e58",
      variationSalt: "7e1d4c90b2a6f3d1",
      inclusionBucket: 1234,
      variationBucket: 4321,
      override: false,
    };
    const r = resolution(detail({ reason: "experiment", variation: "control", rulesetVersion: 1, experiment }));
    expect(r.flagMetadata).toStrictEqual({
      flagnetic_reason: "experiment",
      stage: "fresh",
      ruleset_version: 1,
      variation: "control",
      overridden: false,
      experiment_key: "checkout_v2",
      experiment_variation: "control",
      experiment_unit_type: "anonymous",
      experiment_unit_id: "u_10482",
      experiment_inclusion_bucket: 1234,
      experiment_variation_bucket: 4321,
      experiment_override: false,
    });
  });

  test("the value passes through unchanged", () => {
    expect(resolution(detail({ reason: "fallthrough", value: { mode: "dark" } })).value).toStrictEqual({ mode: "dark" });
  });
});

describe("the evaluation context", () => {
  test("the targeting key is the unit ID of the provider's unit type", () => {
    expect(toFlagneticContext({ targetingKey: "u-1001" }, "user")).toStrictEqual({ units: { user: "u-1001" }, attributes: {} });
    expect(toFlagneticContext({ targetingKey: "d-1" }, "device")).toStrictEqual({ units: { device: "d-1" }, attributes: {} });
  });

  test("an empty or missing targeting key names no unit", () => {
    expect(toFlagneticContext({ targetingKey: "" }, "user")).toStrictEqual({ units: {}, attributes: {} });
    expect(toFlagneticContext({}, "user")).toStrictEqual({ units: {}, attributes: {} });
  });

  test("the units attribute adds unit IDs by unit type; an ID that is not a string is left out", () => {
    expect(toFlagneticContext({ targetingKey: "u-1001", units: { anonymous: "d-1", device: 42 } }, "user")).toStrictEqual({
      units: { user: "u-1001", anonymous: "d-1" },
      attributes: {},
    });
  });

  test("the targeting key wins over a units entry of the same unit type", () => {
    expect(toFlagneticContext({ targetingKey: "u-1", units: { user: "u-2" } }, "user").units).toStrictEqual({ user: "u-1" });
  });

  test("units that are not a map are ignored", () => {
    expect(toFlagneticContext({ targetingKey: "u-1", units: "u-2" }, "user")).toStrictEqual({ units: { user: "u-1" }, attributes: {} });
    expect(toFlagneticContext({ units: ["u-2"] }, "user")).toStrictEqual({ units: {}, attributes: {} });
  });

  test("every other attribute is an attribute", () => {
    expect(toFlagneticContext({ targetingKey: "u-1", units: { anonymous: "d-1" }, country: "DE", vip: true, age: 30 }, "user").attributes).toStrictEqual({
      country: "DE",
      vip: true,
      age: 30,
    });
  });

  test("a date travels as an RFC 3339 string, nested ones included", () => {
    const at = new Date("2026-01-15T10:00:00.000Z");
    expect(toFlagneticContext({ signedUpAt: at, history: [at, "x"], profile: { since: at } }, "user").attributes).toStrictEqual({
      signedUpAt: "2026-01-15T10:00:00.000Z",
      history: ["2026-01-15T10:00:00.000Z", "x"],
      profile: { since: "2026-01-15T10:00:00.000Z" },
    });
  });
});

describe("track details", () => {
  test("exposure reads the flag attribute", () => {
    expect(trackedFlag({ flag: "checkout" })).toBe("checkout");
  });

  test("exposure without a flag, or with an empty or non-string one, names nothing", () => {
    expect(trackedFlag({})).toBeUndefined();
    expect(trackedFlag({ flag: "" })).toBeUndefined();
    expect(trackedFlag({ flag: 42 })).toBeUndefined();
    expect(trackedFlag(undefined)).toBeUndefined();
  });

  test("alias reads the four unit attributes", () => {
    expect(trackedAlias({ fromUnitType: "anonymous", fromUnitId: "d-1", toUnitType: "user", toUnitId: "u-1" })).toStrictEqual({
      fromUnitType: "anonymous",
      fromUnitId: "d-1",
      toUnitType: "user",
      toUnitId: "u-1",
    });
  });

  test("alias missing an attribute, or with an empty or non-string one, names nothing", () => {
    expect(trackedAlias({ fromUnitType: "anonymous", fromUnitId: "d-1", toUnitType: "user" })).toBeUndefined();
    expect(trackedAlias({ fromUnitType: "anonymous", fromUnitId: "d-1", toUnitType: "user", toUnitId: "" })).toBeUndefined();
    expect(trackedAlias({ fromUnitType: "anonymous", fromUnitId: "d-1", toUnitType: "user", toUnitId: 42 })).toBeUndefined();
    expect(trackedAlias(undefined)).toBeUndefined();
  });
});
