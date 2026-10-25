// FlagneticWebProvider registered with the real @openfeature/web-sdk,
// offline: resolution and track as an application sees them through
// OpenFeature's own client. No network, so this runs in Node and in the
// browser; test/openfeature-web.node.test.ts covers events and context
// changes against an evaluation endpoint.
import { ErrorCode, OpenFeature } from "@openfeature/web-sdk";
import { afterEach, describe, expect, test } from "vitest";
import { FlagneticWebProvider } from "../src/openfeature.js";

const cleanups: (() => Promise<void>)[] = [];
afterEach(async () => {
  for (const c of cleanups.splice(0).reverse()) await c();
  await OpenFeature.clearProviders();
});

const flag = (type: string, value: unknown, extra: Record<string, unknown> = {}): Record<string, unknown> => ({
  type,
  value,
  variation: "v",
  reason: "targeting_match",
  override_influenced: false,
  automatic_exposure: false,
  ...extra,
});

// What the server answers a client key: targeting decided every value, so
// every reason reads targeting_match, except the experiment's assignment.
const values = {
  schema_version: 1,
  ruleset_version: 4,
  environment: { key: "production", protected: false },
  flags: {
    checkout: flag("boolean", true, { variation: "on" }),
    paywall_copy: flag("string", "Try free"),
    theme: flag("json", { mode: "dark" }),
    pricing: flag("string", "treatment", {
      variation: "treatment",
      reason: "experiment",
      experiment: {
        key: "pricing_v2",
        variation: "treatment",
        unit_type: "anonymous",
        unit_id: "d-8f1c",
        inclusion_salt: "a1b2c3d4e5f60718",
        variation_salt: "c3d4e5f607182930",
        inclusion_bucket: 3537,
        variation_bucket: 2898,
        override: false,
      },
    }),
  },
};

async function register() {
  const provider = new FlagneticWebProvider("", "", { offline: values, store: false });
  cleanups.push(() => provider.onClose());
  await OpenFeature.setProviderAndWait(provider, { targetingKey: "u-1", units: { anonymous: "d-8f1c" } });
  return { provider, client: OpenFeature.getClient() };
}

describe("resolution", () => {
  test("a value targeting decided is TARGETING_MATCH, the Flagnetic reason in the metadata", async () => {
    const { client } = await register();
    const d = client.getBooleanDetails("checkout", false);
    expect(d).toMatchObject({ value: true, variant: "on", reason: "TARGETING_MATCH" });
    expect(d.flagMetadata).toStrictEqual({ flagnetic_reason: "targeting_match", stage: "fresh", ruleset_version: 4, variation: "on", overridden: false });
  });

  test("strings and JSON values resolve", async () => {
    const { client } = await register();
    expect(client.getStringValue("paywall_copy", "")).toBe("Try free");
    expect(client.getObjectValue("theme", { mode: "system" })).toStrictEqual({ mode: "dark" });
  });

  test("an experiment's assignment is SPLIT, its block in the metadata without salts", async () => {
    const { client } = await register();
    const d = client.getStringDetails("pricing", "control");
    expect(d).toMatchObject({ value: "treatment", variant: "treatment", reason: "SPLIT" });
    expect(d.flagMetadata).toMatchObject({ flagnetic_reason: "experiment", experiment_key: "pricing_v2", experiment_unit_id: "d-8f1c", experiment_inclusion_bucket: 3537 });
    expect(Object.keys(d.flagMetadata).filter((k) => k.includes("salt"))).toStrictEqual([]);
  });

  test("an unknown flag serves the default: ERROR with FLAG_NOT_FOUND", async () => {
    const { client } = await register();
    expect(client.getStringDetails("no_such_flag", "default")).toMatchObject({
      value: "default",
      reason: "ERROR",
      errorCode: ErrorCode.FLAG_NOT_FOUND,
      errorMessage: "flag_not_found",
    });
  });

  test("a flag of another type serves the default: ERROR with TYPE_MISMATCH", async () => {
    const { client } = await register();
    expect(client.getNumberDetails("paywall_copy", 1)).toMatchObject({ value: 1, reason: "ERROR", errorCode: ErrorCode.TYPE_MISMATCH });
  });
});

describe("track", () => {
  test("exposure reports the exposure of what the flag served for the current context", async () => {
    const { provider, client } = await register();
    client.getStringValue("pricing", "control");
    client.track("exposure", { flag: "pricing" });
    expect(provider.client.offline?.recorded()).toMatchObject([{ type: "exposure", flag_key: "pricing", experiment_key: "pricing_v2", variation_key: "treatment" }]);
  });

  test("alias records the alias its four unit attributes name", async () => {
    const { provider, client } = await register();
    client.track("alias", { fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1" });
    expect(provider.client.offline?.recorded()).toMatchObject([{ type: "alias", from_unit_id: "d-8f1c", to_unit_id: "u-1" }]);
  });

  test("another event name, or one missing what it needs, does nothing", async () => {
    const { provider, client } = await register();
    client.getStringValue("pricing", "control");
    client.track("purchase", { flag: "pricing" });
    client.track("exposure", {});
    client.track("alias", { fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user" });
    expect(provider.client.offline?.recorded()).toStrictEqual([]);
  });
});
