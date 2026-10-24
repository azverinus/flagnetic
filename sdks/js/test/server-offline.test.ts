// Offline mode, the getters, local overrides and the change callback of
// flagnetic/server, observed through the public API and the logger hook.
// No network: these run in the browser environment too.
import { describe, expect, test, vi } from "vitest";
import {
  consoleLogger,
  createServer,
  LocalOverrideRefused,
  LocalOverridesNotAllowed,
  testServer,
  testServerFromValues,
  type LogEvent,
  type Logger,
  type LogLevel,
} from "../src/server.js";

interface Logged {
  level: LogLevel;
  event: LogEvent;
  fields: Readonly<Record<string, unknown>>;
}

function captureLogs(): { logger: Logger; logs: Logged[]; events: (name: LogEvent) => Logged[] } {
  const logs: Logged[] = [];
  return {
    logger: (level, event, fields) => logs.push({ level, event, fields }),
    logs,
    events: (name) => logs.filter((l) => l.event === name),
  };
}

const user = { units: { user: "u-1" } };

/** A unit the experiment below includes; it is assigned control. */
const included = { units: { anonymous: "u_10482" } };
/** A unit outside the traffic allocation: it falls through, so no experiment block. */
const outside = { units: { anonymous: "u_10483" } };

/** One flag on one running experiment, opted into automatic exposure or not. */
function experimentDocument(automaticExposure = false): Record<string, unknown> {
  return {
    schema_version: 1,
    version: 3,
    scope: "server",
    environment: { key: "development", protected: false },
    unit_types: ["anonymous"],
    attributes: [],
    segments: [],
    flags: [
      {
        key: "checkout_v2",
        type: "string",
        automatic_exposure: automaticExposure,
        variations: [
          { key: "control", value: "control" },
          { key: "treatment", value: "treatment" },
          { key: "off", value: "off" },
        ],
        on: true,
        off_variation: "off",
        fallthrough: "off",
        prerequisites: [],
        overrides: [],
        rules: [
          {
            id: "r_exp",
            conditions: [],
            serve: {
              kind: "experiment",
              experiment: "checkout_v2",
              unit_type: "anonymous",
              inclusion_salt: "a3f9c2e17b4d0e58",
              variation_salt: "7e1d4c90b2a6f3d1",
              traffic_allocation: 7000,
              weights: [
                { variation: "control", weight: 5000 },
                { variation: "treatment", weight: 5000 },
              ],
            },
          },
        ],
      },
    ],
  };
}

function document(extra: { protected?: boolean } = {}): Record<string, unknown> {
  return {
    schema_version: 1,
    version: 7,
    scope: "server",
    environment: { key: "development", protected: extra.protected ?? false },
    unit_types: ["user"],
    attributes: [{ key: "country", type: "string" }],
    segments: [
      {
        key: "germans",
        private: true,
        conditions: [{ kind: "attribute", attribute: "country", op: "eq", value: "DE" }],
      },
    ],
    flags: [
      {
        key: "checkout",
        type: "boolean",
        on: true,
        off_variation: "off",
        fallthrough: "off",
        variations: [
          { key: "on", value: true },
          { key: "off", value: false },
        ],
        rules: [{ id: "r-de", conditions: [{ kind: "in_segment", segment: "germans" }], serve: { kind: "variation", variation: "on" } }],
      },
      {
        key: "banner",
        type: "boolean",
        on: true,
        off_variation: "off",
        fallthrough: "off",
        prerequisites: [{ flag: "checkout", variation: "on" }],
        variations: [
          { key: "on", value: true },
          { key: "off", value: false },
        ],
        rules: [{ id: "r-all", conditions: [], serve: { kind: "variation", variation: "on" } }],
      },
      { key: "copy", type: "string", on: true, off_variation: "a", fallthrough: "b", variations: [{ key: "a", value: "Buy" }, { key: "b", value: "Try free" }] },
      { key: "limit", type: "number", on: true, off_variation: "a", fallthrough: "a", variations: [{ key: "a", value: 12.5 }] },
      { key: "layout", type: "json", on: true, off_variation: "a", fallthrough: "a", variations: [{ key: "a", value: { columns: [1, 2] } }] },
      {
        key: "pricing",
        type: "string",
        on: true,
        off_variation: "control",
        fallthrough: "control",
        variations: [{ key: "control", value: "old" }, { key: "treatment", value: "new" }],
        rules: [
          {
            id: "r-exp",
            conditions: [],
            serve: {
              kind: "experiment",
              experiment: "pricing_exp",
              unit_type: "user",
              inclusion_salt: "a1b2c3d4e5f60718",
              variation_salt: "c3d4e5f607182930",
              traffic_allocation: 10000,
              weights: [{ variation: "control", weight: 5000 }, { variation: "treatment", weight: 5000 }],
            },
          },
        ],
      },
      { key: "broken", type: "boolean", on: true, off_variation: "x", fallthrough: "x", variations: [{ key: "x", value: true }], rules: [{ id: "r", conditions: [{ kind: "attribute", attribute: "country", op: "resembles", value: "DE" }], serve: { kind: "variation", variation: "x" } }] },
    ],
  };
}

describe("offline mode", () => {
  test("is fresh and ready at once, from text, bytes or an object, with no key", async () => {
    const text = JSON.stringify(document());
    for (const offline of [text, new TextEncoder().encode(text), document()]) {
      const c = createServer("", "", { offline, logger: () => undefined });
      expect(c.stage).toBe("fresh");
      await expect(c.ready(0)).resolves.toBe("fresh");
      expect(c.getBool({ units: {}, attributes: { country: "DE" } }, "checkout", false)).toBe(true);
    }
  });

  test("an unreadable offline document throws at construction", () => {
    expect(() => createServer("", "", { offline: "{" })).toThrow();
    expect(() => createServer("", "", { offline: { schema_version: 2, version: 1, scope: "server" } })).toThrow();
  });

  test("ruleset_loaded carries the source, version and flag count", () => {
    const { logger, events } = captureLogs();
    createServer("", "", { offline: document(), logger });
    expect(events("ruleset_loaded")).toStrictEqual([
      { level: "info", event: "ruleset_loaded", fields: { source: "offline", ruleset_version: 7, flags: 7 } },
    ]);
  });

  test("the recorder counts every evaluation by flag, variation and reason, fallback included", () => {
    const c = testServer(document());
    c.getBool({ attributes: { country: "DE" } }, "checkout", false);
    c.getBool({ attributes: { country: "DE" } }, "checkout", false);
    c.getBool({}, "checkout", false);
    c.getBool({}, "missing", true);
    expect(c.recorder.summary()).toStrictEqual([
      { flag: "checkout", variation: "off", reason: "fallthrough", count: 1 },
      { flag: "checkout", variation: "on", reason: "rule", count: 2 },
      { flag: "missing", reason: "fallback", count: 1 },
    ]);
    c.recorder.reset();
    expect(c.recorder.summary()).toStrictEqual([]);
  });

  test("the recorder holds the exposures and aliases an offline client would have sent", () => {
    const c = testServer(experimentDocument());
    // An exposure reports the assignment an evaluation served.
    c.getString(included, "checkout_v2", "none");
    c.exposure(included, "checkout_v2");
    // The same assignment again is suppressed, and a flag without an
    // experiment block emits nothing.
    c.exposure(included, "checkout_v2");
    c.getString(outside, "checkout_v2", "none");
    c.exposure(outside, "checkout_v2");
    c.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });

    expect(c.recorder.countsByType()).toStrictEqual({ exposure: 1, alias: 1 });
    expect(c.recorder.exposures()[0]).toMatchObject({
      type: "exposure",
      experiment_key: "checkout_v2",
      flag_key: "checkout_v2",
      variation_key: "control",
      unit_type: "anonymous",
      unit_id: "u_10482",
      evaluation_reason: "experiment",
      ruleset_version: 3,
      override: false,
    });
    expect(c.recorder.aliases()[0]).toMatchObject({ from_unit_id: "d-8f1c", to_unit_id: "u-1001" });
    expect(c.recorder.recorded()).toHaveLength(2);
    c.recorder.reset();
    expect(c.recorder.recorded()).toStrictEqual([]);
  });

  test("automatic exposure records one exposure per assignment, and a flag without it records none", () => {
    const on = testServer(experimentDocument(true));
    for (let n = 0; n < 3; n++) on.getString(included, "checkout_v2", "none");
    on.getString(outside, "checkout_v2", "none");
    expect(on.recorder.countsByType()).toStrictEqual({ exposure: 1 });

    const off = testServer(experimentDocument());
    for (let n = 0; n < 3; n++) off.getString(included, "checkout_v2", "none");
    expect(off.recorder.countsByType()).toStrictEqual({});
  });

  test("with sendExposures off nothing is recorded and the client warns once", () => {
    const { logger, events } = captureLogs();
    const c = testServer(experimentDocument(true), { sendExposures: false, logger });
    c.getString(included, "checkout_v2", "none");
    c.exposure(included, "checkout_v2");
    c.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    expect(c.recorder.recorded()).toStrictEqual([]);
    expect(events("exposures_off")).toHaveLength(1);
  });

  test("flush and close resolve at once and make no request", async () => {
    const c = testServer(experimentDocument(true));
    c.getString(included, "checkout_v2", "none");
    await expect(c.flush()).resolves.toBeUndefined();
    await expect(c.close()).resolves.toBeUndefined();
    expect(c.recorder.countsByType()).toStrictEqual({ exposure: 1 });
  });
});

describe("getters", () => {
  const c = createServer("", "", { offline: document(), logger: () => undefined });

  test("each typed getter serves its flag", () => {
    expect(c.getBool({ attributes: { country: "DE" } }, "checkout", false)).toBe(true);
    expect(c.getString(user, "copy", "x")).toBe("Try free");
    expect(c.getNumber(user, "limit", 0)).toBe(12.5);
    expect(c.getJson(user, "layout", null)).toStrictEqual({ columns: [1, 2] });
    expect(Object.isFrozen(c.getJson(user, "layout", null))).toBe(true);
  });

  test("the detail carries value, reason, variation, rule, version, receipt time, stage and the experiment block", () => {
    const d = c.getDetail({ units: { user: "u-1001" } }, "pricing", "none");
    expect(d).toMatchObject({
      reason: "experiment",
      ruleId: "r-exp",
      rulesetVersion: 7,
      stage: "fresh",
      overridden: false,
      experiment: {
        key: "pricing_exp",
        unitType: "user",
        unitId: "u-1001",
        inclusionSalt: "a1b2c3d4e5f60718",
        variationSalt: "c3d4e5f607182930",
        override: false,
      },
    });
    expect(d.variation).toBe(d.experiment?.variation);
    expect(d.value).toBe(d.variation === "control" ? "old" : "new");
    expect(d.receivedAt).toBeInstanceOf(Date);
    expect(d.experiment?.inclusionBucket).toBeTypeOf("number");
  });

  test("a missing flag, a type mismatch and an evaluator error serve the default with fallback", () => {
    expect(c.getDetail(user, "nope", 3)).toMatchObject({ value: 3, reason: "fallback", errorCode: "flag_not_found", stage: "fresh" });
    expect(c.getDetail(user, "copy", 3)).toMatchObject({ value: 3, reason: "fallback", errorCode: "type_mismatch" });
    expect(c.getDetail(user, "broken", false)).toMatchObject({ value: false, reason: "fallback", errorCode: "inconsistent_ruleset" });
    expect(c.getDetail(user, "nope", 3).variation).toBeUndefined();
  });

  test("a type mismatch is logged once per key, an unsupported rule once per flag", () => {
    const { logger, events } = captureLogs();
    const d = createServer("", "", { offline: document(), logger });
    d.getNumber(user, "copy", 1);
    d.getNumber(user, "copy", 1);
    d.getBool(user, "broken", false);
    d.getBool(user, "broken", false);
    expect(events("type_mismatch").map((l) => l.fields)).toStrictEqual([{ flag: "copy", flag_type: "string", requested_type: "number" }]);
    expect(events("rule_unsupported").map((l) => l.fields)).toStrictEqual([{ flag: "broken", error_code: "inconsistent_ruleset" }]);
  });

  test("getters are total: garbage contexts, a throwing logger and hostile values never throw", () => {
    const d = createServer("", "", {
      offline: document(),
      logger: () => {
        throw new Error("logger down");
      },
    });
    const hostile = { get units(): never { throw new Error("boom"); } };
    expect(d.getBool(hostile as never, "checkout", true)).toBe(true);
    expect(d.getDetail(hostile as never, "checkout", true)).toMatchObject({ reason: "fallback", errorCode: "general" });
    expect(d.getBool(undefined as never, "checkout", true)).toBe(false);
    expect(d.getString(null as never, "copy", "x")).toBe("Try free");
    expect(d.getNumber(user, "copy", 1)).toBe(1);
  });

  test("a client key fails construction loudly; any other key does not", () => {
    expect(() => createServer("fn_cli_abc", "http://127.0.0.1:1")).toThrow(/server key/);
  });
});

describe("the default logger", () => {
  test("writes warn and above to the console, named and with its fields", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    const info = vi.spyOn(console, "info").mockImplementation(() => undefined);
    try {
      const c = createServer("", "", { offline: document(), allowOverrides: true });
      c.getNumber(user, "copy", 1);
      expect(info).not.toHaveBeenCalled();
      expect(warn.mock.calls).toStrictEqual([
        ["flagnetic: local_overrides_allowed", {}],
        ["flagnetic: type_mismatch", { flag: "copy", flag_type: "string", requested_type: "number" }],
      ]);
      consoleLogger("info")("info", "ruleset_loaded", { flags: 1 });
      expect(info.mock.calls).toStrictEqual([["flagnetic: ruleset_loaded", { flags: 1 }]]);
    } finally {
      warn.mockRestore();
      info.mockRestore();
    }
  });
});

describe("local overrides", () => {
  test("are refused without the option", () => {
    const { logger, events } = captureLogs();
    const c = createServer("", "", { offline: document(), logger });
    expect(() => {
      c.overrides.set("copy", "local");
    }).toThrow(LocalOverridesNotAllowed);
    expect(c.getDetail(user, "copy", "x").reason).toBe("fallthrough");
    expect(events("override_refused").map((l) => l.fields)).toStrictEqual([{ flag: "copy", cause: "not_allowed" }]);
  });

  test("the option logs a warning at construction", () => {
    const { logger, events } = captureLogs();
    createServer("", "", { offline: document(), logger, allowOverrides: true });
    expect(events("local_overrides_allowed")).toHaveLength(1);
    expect(events("local_overrides_allowed")[0]?.level).toBe("warn");
  });

  test("an overridden flag reads local_override with no experiment block, stage unchanged, counted in the recorder", () => {
    const { logger, events } = captureLogs();
    const c = testServer(document(), { logger, allowOverrides: true });
    const changes: string[][] = [];
    c.onChange((keys) => changes.push(keys));
    c.overrides.set("pricing", "forced");
    const d = c.getDetail({ units: { user: "u-1001" } }, "pricing", "none");
    expect(d).toStrictEqual({
      value: "forced",
      reason: "local_override",
      stage: "fresh",
      overridden: false,
      rulesetVersion: 7,
      receivedAt: d.receivedAt,
    });
    expect(c.overrides.list()).toStrictEqual({ pricing: "forced" });
    expect(c.recorder.summary()).toStrictEqual([{ flag: "pricing", reason: "local_override", count: 1 }]);
    expect(events("override_set").map((l) => l.fields)).toStrictEqual([{ flag: "pricing" }]);

    c.overrides.remove("pricing");
    c.overrides.remove("pricing");
    expect(c.getDetail(user, "pricing", "none").reason).toBe("experiment");
    c.overrides.set("copy", "a");
    c.overrides.set("limit", 1);
    c.overrides.clear();
    expect(changes).toStrictEqual([["pricing"], ["pricing"], ["copy"], ["limit"], ["copy", "limit"]]);
    expect(c.overrides.list()).toStrictEqual({});
  });

  test("a value of another type than the flag's is a TypeError; an unknown key takes the value's type", () => {
    const c = createServer("", "", { offline: document(), logger: () => undefined, allowOverrides: true });
    expect(() => {
      c.overrides.set("copy", 1);
    }).toThrow(TypeError);
    c.overrides.set("new_flag", { a: 1 });
    expect(c.getJson(user, "new_flag", null)).toStrictEqual({ a: 1 });
    expect(c.getDetail(user, "new_flag", false)).toMatchObject({ reason: "fallback", errorCode: "type_mismatch" });
  });

  test("a protected environment refuses them, and a protected ruleset arriving drops those set", () => {
    const { logger, events } = captureLogs();
    const prod = createServer("", "", { offline: document({ protected: true }), logger, allowOverrides: true });
    expect(() => {
      prod.overrides.set("copy", "x");
    }).toThrow(LocalOverrideRefused);
    expect(events("override_refused").map((l) => l.fields)).toStrictEqual([
      { flag: "copy", cause: "protected_environment", environment: "development" },
    ]);

    let protectedNow = false;
    const c = testServer(() => document({ protected: protectedNow }), { logger, allowOverrides: true });
    c.overrides.set("copy", "local");
    const changes: string[][] = [];
    c.onChange((keys) => changes.push(keys));
    protectedNow = true;
    c.reload();
    expect(c.getString(user, "copy", "x")).toBe("Try free");
    expect(c.overrides.list()).toStrictEqual({});
    expect(changes).toStrictEqual([["copy"]]);
  });
});

describe("the change callback", () => {
  test("names flags affected through a segment, a declaration and a prerequisite; an unchanged ruleset fires nothing", () => {
    let doc = document();
    const c = testServer(() => doc);
    const changes: string[][] = [];
    c.onChange((keys) => changes.push(keys));

    c.reload();
    expect(changes).toStrictEqual([]);

    doc = {
      ...doc,
      segments: [{ key: "germans", private: true, conditions: [{ kind: "attribute", attribute: "country", op: "eq", value: "FR" }] }],
    };
    c.reload();
    expect(changes.pop()).toStrictEqual(["banner", "checkout"]);
    expect(c.getBool({ attributes: { country: "DE" } }, "checkout", false)).toBe(false);

    doc = { ...doc, attributes: [{ key: "country", type: "semver" }] };
    c.reload();
    // broken reads the attribute directly.
    expect(changes.pop()).toStrictEqual(["banner", "broken", "checkout"]);
  });

  test("a throwing listener is logged as listener_failed and the others still run", () => {
    const { logger, events } = captureLogs();
    const c = testServerFromValues({ a: true }, { logger });
    const seen: string[][] = [];
    c.onChange(() => {
      throw new Error("listener bug");
    });
    const off = c.onChange((keys) => seen.push(keys));
    c.set("a", false);
    off();
    c.set("a", true);
    expect(seen).toStrictEqual([["a"]]);
    expect(events("listener_failed")).toHaveLength(2);
  });
});

describe("test helpers", () => {
  test("from values: each value is a one-variation flag served as fallthrough", () => {
    const c = testServerFromValues({ on: true, copy: "hi", n: 2, obj: { k: [1] } });
    expect(c.getDetail({}, "on", false)).toMatchObject({ value: true, reason: "fallthrough", variation: "value" });
    expect(c.getString({}, "copy", "")).toBe("hi");
    expect(c.getNumber({}, "n", 0)).toBe(2);
    expect(c.getJson({}, "obj", null)).toStrictEqual({ k: [1] });
  });

  test("set rewrites a flag keeping its type, reload restores the source", () => {
    const c = testServer(document());
    const changes: string[][] = [];
    c.onChange((keys) => changes.push(keys));
    c.set("checkout", true);
    expect(c.getDetail({}, "checkout", false)).toMatchObject({ value: true, reason: "fallthrough" });
    expect(changes).toStrictEqual([["banner", "checkout"]]);
    expect(() => { c.set("copy", 1); }).toThrow(TypeError);
    c.set("layout", "any JSON");
    expect(c.getJson({}, "layout", null)).toBe("any JSON");
    c.reload();
    expect(c.getBool({}, "checkout", false)).toBe(false);
  });

  test("an offline client needs no network: flush and close resolve, reload works only offline", async () => {
    const c = testServerFromValues({ a: true });
    await expect(c.flush()).resolves.toBeUndefined();
    await expect(c.close()).resolves.toBeUndefined();
    expect(c.getBool({}, "a", false)).toBe(true);
  });
});
