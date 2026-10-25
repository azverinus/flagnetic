// FlagneticServerProvider registered with the real @openfeature/server-sdk:
// resolution, track and provider events as an application sees them through
// OpenFeature's own client, offline and against a mock platform.
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { ErrorCode, OpenFeature, ProviderEvents, type EventDetails } from "@openfeature/server-sdk";
import { afterEach, describe, expect, test } from "vitest";
import { FlagneticServerProvider, type FlagneticServerProviderOptions } from "../src/openfeature.js";
import type { LogEvent } from "../src/server.js";
import { ruleset } from "./support/flags-fixture.js";
import { MockPlatform } from "./support/mock-platform.js";

const key = "fn_srv_0123456789abcdefghijklmnopqrstuvwxyzABCD";

// Two flags on one running experiment of unit type anonymous: u_10482 is
// assigned control.
const experiments = JSON.parse(
  readFileSync(fileURLToPath(new URL("../../../conformance/fixtures/experiments/automatic-exposure.json", import.meta.url)), "utf8"),
) as { ruleset: Record<string, unknown> };

const cleanups: (() => Promise<void>)[] = [];
afterEach(async () => {
  for (const c of cleanups.splice(0).reverse()) await c();
  await OpenFeature.clearProviders();
  OpenFeature.clearHandlers();
});

async function register(sdkKey: string, baseUrl: string, options: FlagneticServerProviderOptions) {
  const provider = new FlagneticServerProvider(sdkKey, baseUrl, options);
  cleanups.push(() => provider.onClose());
  await OpenFeature.setProviderAndWait(provider);
  return { provider, client: OpenFeature.getClient() };
}

async function platform(document: Record<string, unknown> = ruleset()): Promise<MockPlatform> {
  const p = await MockPlatform.start(document);
  cleanups.push(() => p.stop());
  return p;
}

/** Resolves with the details of the next event of type. */
function next(type: ProviderEvents): Promise<EventDetails | undefined> {
  return new Promise((resolve) => {
    const handler = (details?: EventDetails): void => {
      OpenFeature.removeHandler(type, handler);
      resolve(details);
    };
    OpenFeature.addHandler(type, handler);
  });
}

/** Records every event of these types, in order. */
function record(...types: ProviderEvents[]): ProviderEvents[] {
  const seen: ProviderEvents[] = [];
  for (const type of types) {
    OpenFeature.addHandler(type, () => {
      seen.push(type);
    });
  }
  return seen;
}

const settled = (ms = 20): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));

describe("resolution", () => {
  test("the targeting key and the attributes decide a rule: TARGETING_MATCH, the Flagnetic reason in the metadata", async () => {
    const { client } = await register("", "", { offline: ruleset() });
    const d = await client.getBooleanDetails("checkout", false, { targetingKey: "u-1", country: "DE" });
    expect(d).toMatchObject({ value: true, variant: "on", reason: "TARGETING_MATCH" });
    expect(d.flagMetadata).toStrictEqual({ flagnetic_reason: "rule", stage: "fresh", ruleset_version: 7, variation: "on", overridden: false });
  });

  test("no rule matching falls through: DEFAULT", async () => {
    const { client } = await register("", "", { offline: ruleset() });
    expect(await client.getBooleanDetails("checkout", false, { targetingKey: "u-1", country: "FR" })).toMatchObject({
      value: false,
      variant: "off",
      reason: "DEFAULT",
    });
  });

  test("strings and JSON values resolve", async () => {
    const { client } = await register("", "", { offline: ruleset() });
    expect(await client.getStringValue("paywall_copy", "", { targetingKey: "u-1" })).toBe("Try free");
    expect(await client.getObjectValue("theme", { mode: "system" }, { targetingKey: "u-1" })).toStrictEqual({ mode: "dark" });
  });

  test("an unknown flag serves the default: ERROR with FLAG_NOT_FOUND", async () => {
    const { client } = await register("", "", { offline: ruleset() });
    const d = await client.getBooleanDetails("no_such_flag", true, { targetingKey: "u-1" });
    expect(d).toMatchObject({ value: true, reason: "ERROR", errorCode: ErrorCode.FLAG_NOT_FOUND, errorMessage: "flag_not_found" });
  });

  test("a flag of another type serves the default: ERROR with TYPE_MISMATCH", async () => {
    const { client } = await register("", "", { offline: ruleset() });
    const d = await client.getNumberDetails("paywall_copy", 1, { targetingKey: "u-1" });
    expect(d).toMatchObject({ value: 1, reason: "ERROR", errorCode: ErrorCode.TYPE_MISMATCH });
  });

  test("an experiment's assignment is SPLIT, its block in the metadata without salts", async () => {
    const { client } = await register("", "", { offline: experiments.ruleset });
    const d = await client.getStringDetails("auto_off", "", { units: { anonymous: "u_10482" } });
    expect(d).toMatchObject({ value: "control", variant: "control", reason: "SPLIT" });
    expect(d.flagMetadata).toStrictEqual({
      flagnetic_reason: "experiment",
      stage: "fresh",
      ruleset_version: 1,
      variation: "control",
      overridden: false,
      experiment_key: "checkout_v2",
      experiment_variation: "control",
      experiment_unit_type: "anonymous",
      experiment_unit_id: "u_10482",
      experiment_inclusion_bucket: 6884,
      experiment_variation_bucket: 3653,
      experiment_override: false,
    });
  });

  test("unitType names the unit type of the targeting key", async () => {
    const { client } = await register("", "", { offline: experiments.ruleset, unitType: "anonymous" });
    expect(await client.getStringDetails("auto_off", "", { targetingKey: "u_10482" })).toMatchObject({ value: "control", reason: "SPLIT" });
  });
});

describe("track", () => {
  test("exposure reports the exposure of what the flag served for the evaluation context", async () => {
    const p = await platform(experiments.ruleset);
    const { provider, client } = await register(key, p.baseUrl, { sendSummaries: false });
    const context = { targetingKey: "u-1", units: { anonymous: "u_10482" } };
    await client.getStringValue("auto_off", "", context);
    client.track("exposure", context, { flag: "auto_off" });
    await provider.client.flush();
    expect(p.events()).toMatchObject([{ type: "exposure", flag_key: "auto_off", experiment_key: "checkout_v2", variation_key: "control", unit_type: "anonymous", unit_id: "u_10482" }]);
  });

  test("alias records the alias its four unit attributes name", async () => {
    const p = await platform(experiments.ruleset);
    const { provider, client } = await register(key, p.baseUrl, { sendSummaries: false });
    client.track("alias", {}, { fromUnitType: "anonymous", fromUnitId: "d-1", toUnitType: "user", toUnitId: "u-1" });
    await provider.client.flush();
    expect(p.events()).toMatchObject([{ type: "alias", from_unit_type: "anonymous", from_unit_id: "d-1", to_unit_type: "user", to_unit_id: "u-1" }]);
  });

  test("another event name, or one missing what it needs, does nothing", async () => {
    const p = await platform(experiments.ruleset);
    const { provider, client } = await register(key, p.baseUrl, { sendSummaries: false });
    const context = { units: { anonymous: "u_10482" } };
    await client.getStringValue("auto_off", "", context);
    client.track("checkout", context, { flag: "auto_off" });
    client.track("exposure", context, {});
    client.track("exposure", context, { flag: 7 });
    client.track("alias", context, { fromUnitType: "anonymous", fromUnitId: "d-1", toUnitType: "user" });
    await provider.client.flush();
    expect(p.events()).toStrictEqual([]);
  });
});

describe("provider events", () => {
  test("READY once the first ruleset arrived", async () => {
    const p = await platform();
    const ready = next(ProviderEvents.Ready);
    const { provider, client } = await register(key, p.baseUrl, {});
    await ready;
    expect(provider.client.stage).toBe("fresh");
    expect(client.providerStatus).toBe("READY");
  });

  test("a changed ruleset sends CONFIGURATION_CHANGED with the keys that may have changed", async () => {
    const p = await platform();
    await register(key, p.baseUrl, {});
    const changed = next(ProviderEvents.ConfigurationChanged);
    const doc = ruleset();
    const theme = (doc["flags"] as { key: string; fallthrough: string }[]).find((f) => f.key === "theme");
    if (theme === undefined) throw new Error("fixture: no theme flag");
    theme.fallthrough = "light";
    p.publish({ ...doc, version: 8 });
    expect((await changed)?.flagsChanged).toContain("theme");
  });

  test("a failing fetch sends STALE, and READY once the client is fresh again", async () => {
    const p = await platform();
    const { client } = await register(key, p.baseUrl, {});
    const stale = next(ProviderEvents.Stale);
    p.rulesetDefault = { kind: "status", status: 500 };
    p.emit(p.version + 1);
    await stale;
    expect(client.providerStatus).toBe("STALE");

    const ready = next(ProviderEvents.Ready);
    p.rulesetDefault = { kind: "current" };
    await ready;
    expect(client.providerStatus).toBe("READY");
  }, 10_000);

  test("a key revoked before initialize resolved: ERROR right after OpenFeature's READY, and the default served", async () => {
    const p = await platform();
    p.rulesetDefault = { kind: "status", status: 401 };
    p.streamDefault = { kind: "status", status: 401 };
    const errored = next(ProviderEvents.Error);
    const { client } = await register(key, p.baseUrl, { readyTimeout: 500, logger: () => undefined });
    expect((await errored)?.errorCode).toBe("GENERAL");
    expect(client.providerStatus).toBe("ERROR");
    expect(await client.getBooleanValue("checkout", true, { targetingKey: "u-1" })).toBe(true);
  });

  test("a key revoked later: ERROR, and no STALE while it stays revoked", async () => {
    const p = await platform();
    const { client } = await register(key, p.baseUrl, { logger: () => undefined });
    const seen = record(ProviderEvents.Error, ProviderEvents.Stale);
    const errored = next(ProviderEvents.Error);
    p.rulesetDefault = { kind: "status", status: 401 };
    p.emit(p.version + 1);
    await errored;
    await settled(100);
    expect(seen).toStrictEqual([ProviderEvents.Error]);
    expect(client.providerStatus).toBe("ERROR");
  });

  test("the logger option keeps receiving every log event", async () => {
    const p = await platform();
    const events: LogEvent[] = [];
    await register(key, p.baseUrl, { logger: (_level, event) => events.push(event) });
    expect(events).toContain("ruleset_loaded");
  });

  test("no event is sent after the provider is closed", async () => {
    const p = await platform();
    const { provider } = await register(key, p.baseUrl, {});
    const seen = record(ProviderEvents.ConfigurationChanged, ProviderEvents.Stale);
    await provider.onClose();
    p.publish({ ...ruleset(), version: 8 });
    await settled(100);
    expect(seen).toStrictEqual([]);
  });
});
