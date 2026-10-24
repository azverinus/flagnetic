// Evaluation summaries, exposures and aliases in server mode against a mock
// platform on Node's http server: the mock scenarios for the ingest
// side of a server SDK (the retry policy, summary counts, the exposure
// shape, auto-exposure, suppression, the queue's bounds and close).
// Intervals are scaled down through the client's test seams; the behaviour
// is the production one.
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, test } from "vitest";
import { createServerWith, type EventTiming } from "../src/server/client.js";
import type { Timing } from "../src/server/delivery.js";
import type { FlagneticServer, LogEvent, LogLevel, ServerOptions } from "../src/server.js";
import { MockPlatform, rulesetOf } from "./support/mock-platform.js";

const key = "fn_srv_0123456789abcdefghijklmnopqrstuvwxyzABCD";

// The conformance fixture's ruleset: two flags on one running experiment,
// one opted into automatic exposure, with an override landing in it.
const experiments = JSON.parse(
  readFileSync(fileURLToPath(new URL("../../../conformance/fixtures/experiments/automatic-exposure.json", import.meta.url)), "utf8"),
) as { ruleset: Record<string, unknown> };

/** A unit the experiment includes, and what it is assigned (the worked example of the experiment). */
const assigned = { units: { anonymous: "u_10482" } };
/** A unit outside the traffic allocation: it falls through, so no experiment block. */
const outside = { units: { anonymous: "u_10483" } };
/** A unit an override pins, whose shadow evaluation lands in the experiment. */
const overridden = { units: { anonymous: "u_10484" } };

const fast: Partial<Timing> = {
  requestTimeout: 500,
  pollConnected: 10_000,
  pollDisconnected: 10_000,
  retryInitial: 40,
  retryMax: 80,
  unauthorizedMax: 160,
};

const fastEvents: Partial<EventTiming> = {
  flushInterval: 40,
  // Long enough that only an explicit flush closes a window, unless a test
  // shortens it.
  summaryInterval: 60_000,
  maxWindowAge: 3_600_000,
  closeTimeout: 500,
};

interface Logged {
  level: LogLevel;
  event: LogEvent;
  fields: Readonly<Record<string, unknown>>;
}

interface Harness {
  client: FlagneticServer;
  logs: Logged[];
  events: (name: LogEvent) => Logged[];
}

const cleanups: (() => Promise<void>)[] = [];
afterEach(async () => {
  for (const c of cleanups.splice(0).reverse()) await c();
});

async function platform(document: Record<string, unknown> = experiments.ruleset): Promise<MockPlatform> {
  const p = await MockPlatform.start(document);
  cleanups.push(() => p.stop());
  return p;
}

function start(
  p: MockPlatform,
  options: ServerOptions = {},
  eventTiming: Partial<EventTiming> = {},
  timing: Partial<Timing> = {},
  random: () => number = Math.random,
): Harness {
  const logs: Logged[] = [];
  const client = createServerWith(
    key,
    p.baseUrl,
    { stream: false, logger: (level, event, fields) => logs.push({ level, event, fields }), ...options },
    { timing: { ...fast, ...timing }, eventTiming: { ...fastEvents, ...eventTiming }, random },
  );
  cleanups.push(() => client.close());
  return { client, logs, events: (name) => logs.filter((l) => l.event === name) };
}

/**
 * A retry delay past the reporting interval, so a report that fails gives
 * up at once rather than retrying the batch until the interval is up.
 */
const giveUpAtOnce: Partial<Timing> = { retryInitial: 120_000, retryMax: 120_000 };

const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

async function eventually(check: () => void | Promise<void>, timeout = 3000): Promise<void> {
  const deadline = Date.now() + timeout;
  for (;;) {
    try {
      await check();
      return;
    } catch (e) {
      if (Date.now() > deadline) throw e;
      await sleep(5);
    }
  }
}

/** The one evaluation summary of a batch. */
function summaryOf(p: MockPlatform, at = 0): Record<string, unknown> {
  const e = p.batches[at]?.envelope.events.find((x) => x.type === "evaluation_summary");
  expect(e, `batch ${String(at)} carries an evaluation summary`).toBeDefined();
  return e as Record<string, unknown>;
}

// ------------------------------------------------------------- summaries

describe("evaluation summaries", () => {
  test("counts every evaluation per flag, variation and reason, fallback included, with no unit ID in the body", async () => {
    const p = await platform();
    const h = start(p, { sendExposures: false });
    await h.client.ready(2_000);

    for (let n = 0; n < 3; n++) h.client.getString(assigned, "auto_on", "none");
    h.client.getString(outside, "auto_on", "none");
    // A flag the ruleset lacks, and a getter of the wrong type: both
    // `fallback`, both counted, neither with a variation.
    h.client.getString(assigned, "missing_flag", "none");
    h.client.getBool(assigned, "auto_on", false);
    await h.client.flush();

    await eventually(() => {
      expect(p.batches).toHaveLength(1);
    });
    const summary = summaryOf(p);
    expect(summary.counts).toStrictEqual([
      { flag: "auto_on", variation: null, reason: "fallback", count: 1 },
      { flag: "auto_on", variation: "control", reason: "experiment", count: 3 },
      { flag: "auto_on", variation: "off", reason: "fallthrough", count: 1 },
      { flag: "missing_flag", variation: null, reason: "fallback", count: 1 },
    ]);
    expect(summary.ruleset_version).toBe(1);
    expect(Date.parse(summary.interval_start as string)).toBeLessThanOrEqual(Date.parse(summary.interval_end as string));
    // No unit ID, unit type or flag value is in an evaluation summary: only
    // flag keys, variation keys and reasons.
    const body = p.batches[0]?.body ?? "";
    for (const absent of ["u_10482", "u_10483", "anonymous"]) {
      expect(body.includes(absent), `${absent} is nowhere in the body`).toBe(false);
    }
  });

  test("a local override is counted under local_override with no variation and the substituted value never leaves", async () => {
    const p = await platform();
    const h = start(p, { allowOverrides: true, sendExposures: false });
    await h.client.ready(2_000);
    h.client.overrides.set("auto_on", "Local banner");
    expect(h.client.getString(assigned, "auto_on", "none")).toBe("Local banner");
    await h.client.flush();

    await eventually(() => {
      expect(p.batches).toHaveLength(1);
    });
    expect(summaryOf(p).counts).toStrictEqual([{ flag: "auto_on", variation: null, reason: "local_override", count: 1 }]);
    expect(p.batches[0]?.body.includes("Local banner")).toBe(false);
  });

  test("nothing evaluated, nothing sent", async () => {
    const p = await platform();
    const h = start(p);
    await h.client.ready(2_000);
    await h.client.flush();
    await sleep(100);
    expect(p.eventsRequests()).toHaveLength(0);
  });

  test("the window closes on the reporting interval without a flush", async () => {
    const p = await platform();
    const h = start(p, { sendExposures: false }, { summaryInterval: 80 });
    await h.client.ready(2_000);
    h.client.getString(assigned, "auto_on", "none");
    await eventually(() => {
      expect(p.batches.length).toBeGreaterThanOrEqual(1);
    });
    expect(summaryOf(p).counts).toStrictEqual([{ flag: "auto_on", variation: "control", reason: "experiment", count: 1 }]);
  });

  test("a failed window merges into the next one, whose interval begins where the failed one did", async () => {
    const p = await platform();
    const h = start(p, { sendExposures: false }, {}, giveUpAtOnce, () => 0.9);
    await h.client.ready(2_000);
    p.eventsReplies.push({ kind: "status", status: 500 });
    h.client.getString(assigned, "auto_on", "none");
    const firstStart = Date.now();
    await h.client.flush();
    await eventually(() => {
      expect(p.batches.length).toBeGreaterThanOrEqual(1);
    });
    expect(h.events("events_failed").length).toBeGreaterThanOrEqual(1);

    // A second window: its report carries both intervals' counts.
    h.client.getString(assigned, "auto_on", "none");
    const before = p.batches.length;
    await h.client.flush();
    await eventually(() => {
      expect(p.batches.length).toBeGreaterThan(before);
    });
    const merged = summaryOf(p, p.batches.length - 1);
    expect(merged.counts).toStrictEqual([{ flag: "auto_on", variation: "control", reason: "experiment", count: 2 }]);
    expect(Date.parse(merged.interval_start as string)).toBeLessThanOrEqual(firstStart);
  });

  test("a window whose interval began over the maximum age ago is dropped, and this interval's own counts are not", async () => {
    const p = await platform();
    const h = start(p, { sendExposures: false }, { maxWindowAge: 60 }, giveUpAtOnce, () => 0.9);
    await h.client.ready(2_000);
    p.eventsReplies.push({ kind: "status", status: 500 });
    h.client.getString(assigned, "auto_on", "none");
    await h.client.flush();
    await eventually(() => {
      expect(p.batches.length).toBeGreaterThanOrEqual(1);
    });

    // Past the maximum window age, the kept window goes; a fresh evaluation
    // in the new window is reported on its own.
    await sleep(80);
    h.client.getString(outside, "auto_on", "none");
    const before = p.batches.length;
    await h.client.flush();
    await eventually(() => {
      expect(p.batches.length).toBeGreaterThan(before);
    });
    expect(summaryOf(p, p.batches.length - 1).counts).toStrictEqual([
      { flag: "auto_on", variation: "off", reason: "fallthrough", count: 1 },
    ]);
    expect(h.events("queue_dropped").map((l) => l.fields.cause)).toContain("window_older_than_one_hour");
  });

  test("sendSummaries off reports nothing", async () => {
    const p = await platform();
    const h = start(p, { sendSummaries: false, sendExposures: false });
    await h.client.ready(2_000);
    h.client.getString(assigned, "auto_on", "none");
    await h.client.flush();
    await sleep(100);
    expect(p.eventsRequests()).toHaveLength(0);
  });
});

// -------------------------------------------------------- the envelope

describe("the ingest envelope", () => {
  test("the envelope, gzipped, with the SDK's identity, User-Agent and schema version", async () => {
    const p = await platform();
    const h = start(p, { platform: "linux", sendExposures: false });
    await h.client.ready(2_000);
    h.client.getString(assigned, "auto_on", "none");
    await h.client.flush();
    await eventually(() => {
      expect(p.batches).toHaveLength(1);
    });
    const batch = p.batches[0];
    expect(batch?.headers.authorization).toBe(`Bearer ${key}`);
    expect(batch?.headers["content-type"]).toBe("application/json");
    expect(batch?.headers["content-encoding"]).toBe("gzip");
    expect(batch?.headers["flagnetic-schema-version"]).toBe("1");
    expect(batch?.headers["user-agent"]).toMatch(/^flagnetic-js\/\S+ \(linux\)$/);
    expect(batch?.envelope.schema_version).toBe(1);
    expect(batch?.envelope.sdk.name).toBe("flagnetic-js");
    expect(batch?.envelope.sdk.platform).toBe("linux");
    expect(Number.isNaN(Date.parse(batch?.envelope.sent_at ?? ""))).toBe(false);
  });
});

// ------------------------------------------------------ the retry policy

describe("the retry policy", () => {
  test("a 5xx retries the whole batch with identical event IDs", async () => {
    const p = await platform();
    const h = start(p);
    await h.client.ready(2_000);
    p.eventsReplies.push({ kind: "status", status: 500 });
    h.client.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    h.client.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1d", toUnitType: "user", toUnitId: "u-1002" });
    await h.client.flush();

    await eventually(() => {
      expect(p.batches.length).toBeGreaterThanOrEqual(2);
    });
    expect(p.batches[0]?.types).toStrictEqual(["alias", "alias"]);
    expect(p.batches[1]?.eventIds).toStrictEqual(p.batches[0]?.eventIds);
    const failed = h.events("events_failed");
    expect(failed[0]?.fields).toMatchObject({ status: 500, events: 2 });
    expect(failed[0]?.fields.retry_in_ms).toBeTypeOf("number");
  });

  test("a 429 retries the whole batch by its own backoff, not after a long Retry-After", async () => {
    const p = await platform();
    const h = start(p, { sendSummaries: false });
    await h.client.ready(2_000);
    p.eventsReplies.push({ kind: "status", status: 429, headers: { "Retry-After": "60" } });
    h.client.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    await h.client.flush();

    await eventually(() => {
      expect(p.batches.length).toBeGreaterThanOrEqual(2);
    }, 5_000);
    expect(p.batches[1]?.eventIds).toStrictEqual(p.batches[0]?.eventIds);
    // The queue's backoff caps at 80 ms here; a header naming a minute does not hold exposures back.
    expect((p.batches[1]?.at ?? 0) - (p.batches[0]?.at ?? 0)).toBeLessThan(2_000);
  });

  test("an accepted answer whose body stalls ends at the request timeout and the queue keeps sending", async () => {
    const p = await platform();
    const h = start(p, { sendSummaries: false });
    await h.client.ready(2_000);
    p.eventsReplies.push({ kind: "stall" });
    h.client.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    await h.client.flush();
    h.client.alias({ fromUnitType: "anonymous", fromUnitId: "d-9a2b", toUnitType: "user", toUnitId: "u-1002" });
    await h.client.flush();

    await eventually(() => {
      expect(p.batches.length).toBe(2);
    });
    // The stalled batch was accepted: only the new alias goes.
    expect(p.batches[1]?.eventIds).toHaveLength(1);
    expect(p.batches[1]?.eventIds).not.toStrictEqual(p.batches[0]?.eventIds);
  });

  test("a network error retries the whole batch", async () => {
    const p = await platform();
    const h = start(p);
    await h.client.ready(2_000);
    p.eventsReplies.push({ kind: "network" });
    h.client.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    await h.client.flush();

    await eventually(() => {
      expect(p.batches.length).toBeGreaterThanOrEqual(2);
    });
    expect(p.batches[1]?.eventIds).toStrictEqual(p.batches[0]?.eventIds);
    expect(h.events("events_failed")[0]?.fields.cause).toMatch(/network error/);
  });

  test("rejected events are never sent again", async () => {
    const p = await platform();
    const h = start(p);
    await h.client.ready(2_000);
    h.client.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    await h.client.flush();
    await eventually(() => {
      expect(p.batches).toHaveLength(1);
    });
    const rejected = p.batches[0]?.eventIds[0] ?? "";
    p.eventsReplies.push({
      kind: "accepted",
      body: JSON.stringify({ accepted: 0, duplicates: 0, dropped_rows: 0, rejected: [{ index: 0, event_id: rejected, code: "event_invalid", detail: "no" }] }),
    });

    // The next batch holds only the new alias: a rejection is final.
    h.client.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1e", toUnitType: "user", toUnitId: "u-1003" });
    await h.client.flush();
    await eventually(() => {
      expect(p.batches.length).toBeGreaterThanOrEqual(2);
    });
    await sleep(150);
    expect(p.events().filter((e) => e.event_id === rejected)).toHaveLength(1);
  });

  test("any other 4xx drops the batch and logs it", async () => {
    const p = await platform();
    const h = start(p);
    await h.client.ready(2_000);
    p.eventsReplies.push({ kind: "status", status: 400 });
    h.client.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    await h.client.flush();

    await eventually(() => {
      expect(h.events("queue_dropped").length).toBeGreaterThanOrEqual(1);
    });
    expect(h.events("queue_dropped")[0]?.fields).toMatchObject({ cause: "client_error", status: 400 });
    await sleep(150);
    expect(p.batches).toHaveLength(1);
  });
});

// ---------------------------------------------------------- exposures

describe("exposures and aliases", () => {
  test("the exposure call emits only with an experiment block, and once per assignment", async () => {
    const p = await platform();
    const h = start(p, { sendSummaries: false });
    await h.client.ready(2_000);

    // An exposure reports the assignment an evaluation served, so
    // the flag is read where its value is used and reported where the unit
    // encounters what it decides.
    h.client.getString(assigned, "auto_off", "none");
    h.client.exposure(assigned, "auto_off");
    // A second call for the same assignment is suppressed in this process.
    h.client.exposure(assigned, "auto_off");
    // No experiment block: outside the allocation, and a flag the ruleset lacks.
    h.client.getString(outside, "auto_off", "none");
    h.client.exposure(outside, "auto_off");
    h.client.exposure(assigned, "missing_flag");
    await h.client.flush();

    await eventually(() => {
      expect(p.batches).toHaveLength(1);
    });
    const events = p.events();
    expect(events).toHaveLength(1);
    expect(events[0]).toMatchObject({
      type: "exposure",
      experiment_key: "checkout_v2",
      flag_key: "auto_off",
      variation_key: "control",
      unit_type: "anonymous",
      unit_id: "u_10482",
      evaluation_reason: "experiment",
      ruleset_version: 1,
      override: false,
      platform: "node",
    });
    expect(events[0]?.event_id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    expect(Number.isNaN(Date.parse(events[0]?.occurred_at as string))).toBe(false);
    // The two mistakes an exposure call with nothing to report can be: a
    // flag evaluated for this context whose evaluation carried no
    // experiment block, and a flag no evaluation of which ever has.
    expect(h.events("exposure_skipped").map((l) => l.fields.cause)).toStrictEqual(["no_detail", "no_experiment"]);
  });

  test("an exposure reports the assignment served, not the ruleset that arrived since", async () => {
    const p = await platform();
    const h = start(p, { stream: true, sendSummaries: false });
    await h.client.ready(2_000);
    await p.until(() => p.openStreams === 1);
    // The application reads the flag and acts on what it was served.
    h.client.getString(assigned, "auto_off", "none");

    // The experiment is stopped before the unit reaches the behaviour the
    // variation decides. The assignment it acted on is still the one to
    // report, with the ruleset version it was served under.
    const fetched = p.rulesetRequests().length;
    p.publish(rulesetOf(2, { auto_off: true }));
    await eventually(() => {
      expect(p.rulesetRequests().length).toBeGreaterThan(fetched);
    });
    h.client.exposure(assigned, "auto_off");
    await h.client.flush();

    await eventually(() => {
      expect(p.batches).toHaveLength(1);
    });
    expect(p.events()).toHaveLength(1);
    expect(p.events()[0]).toMatchObject({
      flag_key: "auto_off",
      experiment_key: "checkout_v2",
      variation_key: "control",
      ruleset_version: 1,
    });

    // Reading it again under the new ruleset forgets the assignment: the
    // call now reports nothing rather than an experiment nobody serves.
    h.client.getBool(assigned, "auto_off", false);
    h.client.exposure(assigned, "auto_off");
    expect(h.events("exposure_skipped").map((l) => l.fields.cause)).toStrictEqual(["no_detail"]);
  });

  test("an override landing in the experiment is exposed and marked", async () => {
    const p = await platform();
    const h = start(p, { sendSummaries: false });
    await h.client.ready(2_000);
    h.client.getString(overridden, "auto_off", "none");
    h.client.exposure(overridden, "auto_off");
    await h.client.flush();
    await eventually(() => {
      expect(p.batches).toHaveLength(1);
    });
    expect(p.events()[0]).toMatchObject({
      evaluation_reason: "override",
      variation_key: "off",
      unit_id: "u_10484",
      override: true,
    });
  });

  test("a different unit of the same experiment is its own exposure", async () => {
    const p = await platform();
    const h = start(p, { sendSummaries: false });
    await h.client.ready(2_000);
    h.client.getString(assigned, "auto_off", "none");
    h.client.getString(overridden, "auto_off", "none");
    h.client.exposure(assigned, "auto_off");
    h.client.exposure(overridden, "auto_off");
    await h.client.flush();
    await eventually(() => {
      expect(p.batches).toHaveLength(1);
    });
    expect(p.events().map((e) => e.unit_id)).toStrictEqual(["u_10482", "u_10484"]);
  });

  test("automatic exposure emits on every evaluation carrying an experiment block, once per assignment", async () => {
    const p = await platform();
    const h = start(p, { sendSummaries: false });
    await h.client.ready(2_000);
    for (let n = 0; n < 3; n++) h.client.getString(assigned, "auto_on", "none");
    // The same flag outside the allocation, and the flag that did not opt in.
    h.client.getString(outside, "auto_on", "none");
    for (let n = 0; n < 3; n++) h.client.getString(assigned, "auto_off", "none");
    await h.client.flush();

    await eventually(() => {
      expect(p.batches).toHaveLength(1);
    });
    expect(p.events().map((e) => e.flag_key)).toStrictEqual(["auto_on"]);
  });

  test("a local override and a fallback never emit, even with automatic exposure", async () => {
    const p = await platform();
    const h = start(p, { sendSummaries: false, allowOverrides: true });
    await h.client.ready(2_000);
    h.client.overrides.set("auto_on", "Local banner");
    h.client.getString(assigned, "auto_on", "none");
    h.client.exposure(assigned, "auto_on");
    // A fallback: the getter asks for another type than the flag's.
    h.client.getBool(assigned, "auto_on", false);
    await h.client.flush();
    await sleep(150);
    expect(p.eventsRequests()).toHaveLength(0);
    expect(h.events("exposure_skipped")[0]?.fields.cause).toBe("local_override");
  });

  test("flush resolves at its timeout while the platform does not answer", async () => {
    const p = await platform();
    const h = start(p, { sendSummaries: false });
    await h.client.ready(2_000);
    p.eventsReplies.push({ kind: "hang" });
    h.client.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    const began = Date.now();
    await h.client.flush(100);
    expect(Date.now() - began).toBeLessThan(1_500);
  });

  test("the alias event carries both unit pairs and changes no context", async () => {
    const p = await platform();
    const h = start(p, { sendSummaries: false });
    await h.client.ready(2_000);
    h.client.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    await h.client.flush();
    await eventually(() => {
      expect(p.batches).toHaveLength(1);
    });
    const e = p.events()[0];
    expect(e).toMatchObject({
      type: "alias",
      from_unit_type: "anonymous",
      from_unit_id: "d-8f1c",
      to_unit_type: "user",
      to_unit_id: "u-1001",
    });
    // The assignment still hashes on the anonymous unit.
    expect(h.client.getDetail(assigned, "auto_off", "none").variation).toBe("control");
  });

  test("sendExposures off warns at construction and sends nothing", async () => {
    const p = await platform();
    const h = start(p, { sendExposures: false, sendSummaries: false });
    await h.client.ready(2_000);
    expect(h.events("exposures_off")).toHaveLength(1);
    h.client.getString(assigned, "auto_on", "none");
    h.client.exposure(assigned, "auto_off");
    h.client.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    await h.client.flush();
    await sleep(150);
    expect(p.eventsRequests()).toHaveLength(0);
  });

  test("the context's predeclared attributes name the platform and app version", async () => {
    const p = await platform();
    const h = start(p, { sendSummaries: false, appVersion: "1.0.0" });
    await h.client.ready(2_000);
    const withAttributes = { units: assigned.units, attributes: { platform: "android", app_version: "1.42.0" } };
    h.client.getString(withAttributes, "auto_off", "none");
    h.client.exposure(withAttributes, "auto_off");
    await h.client.flush();
    await eventually(() => {
      expect(p.batches).toHaveLength(1);
    });
    expect(p.events()[0]).toMatchObject({ platform: "android", app_version: "1.42.0" });
  });
});

// -------------------------------------------------------- queue bounds

describe("the queue's bounds", () => {
  test("the batch size flushes at once and bounds a batch", async () => {
    const p = await platform(rulesetOf(1, {}));
    const h = start(p, { sendSummaries: false }, { batchSize: 4, flushInterval: 60_000 });
    await h.client.ready(2_000);
    for (let n = 0; n < 4; n++) {
      h.client.alias({ fromUnitType: "anonymous", fromUnitId: `d-${String(n)}`, toUnitType: "user", toUnitId: `u-${String(n)}` });
    }
    // No flush call and no interval: the batch size alone sent it.
    await eventually(() => {
      expect(p.batches).toHaveLength(1);
    });
    expect(p.batches[0]?.types).toHaveLength(4);
  });

  test("the flush interval sends what is queued", async () => {
    const p = await platform(rulesetOf(1, {}));
    const h = start(p, { sendSummaries: false }, { flushInterval: 60 });
    await h.client.ready(2_000);
    h.client.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    await eventually(() => {
      expect(p.batches).toHaveLength(1);
    });
  });

  test("a full queue drops its oldest events and logs queue_dropped", async () => {
    const p = await platform(rulesetOf(1, {}));
    const h = start(p, { sendSummaries: false }, { capacity: 3, batchSize: 100, flushInterval: 60_000 });
    await h.client.ready(2_000);
    for (let n = 0; n < 5; n++) {
      h.client.alias({ fromUnitType: "anonymous", fromUnitId: `d-${String(n)}`, toUnitType: "user", toUnitId: `u-${String(n)}` });
    }
    expect(h.events("queue_dropped")).toHaveLength(2);
    expect(h.events("queue_dropped")[0]?.fields).toMatchObject({ dropped: 1, capacity: 3, cause: "queue_full" });
    await h.client.flush();
    await eventually(() => {
      expect(p.batches).toHaveLength(1);
    });
    expect(p.events().map((e) => e.from_unit_id)).toStrictEqual(["d-2", "d-3", "d-4"]);
  });
});

// --------------------------------------------------------------- close

describe("close", () => {
  test("close flushes what is queued and resolves within its timeout", async () => {
    const p = await platform();
    const h = start(p);
    await h.client.ready(2_000);
    h.client.getString(assigned, "auto_on", "none");
    h.client.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });

    const began = Date.now();
    await h.client.close();
    expect(Date.now() - began).toBeLessThan(500);
    // auto_on opted into automatic exposure, so the evaluation emitted one.
    expect(p.events().map((e) => e.type).sort()).toStrictEqual(["alias", "evaluation_summary", "exposure"]);
  });

  test("close sends every batch the queue holds, not only the first", async () => {
    const p = await platform(rulesetOf(1, {}));
    const h = start(p, { sendSummaries: false }, { batchSize: 2, flushInterval: 60_000 });
    await h.client.ready(2_000);
    for (let n = 0; n < 5; n++) {
      h.client.alias({ fromUnitType: "anonymous", fromUnitId: `d-${String(n)}`, toUnitType: "user", toUnitId: `u-${String(n)}` });
    }
    await h.client.close();
    expect(p.events().map((e) => e.from_unit_id)).toStrictEqual(["d-0", "d-1", "d-2", "d-3", "d-4"]);
  });

  test("close resolves promptly while a batch is backing off, and sends it once more", async () => {
    const p = await platform();
    const h = start(p, { sendSummaries: false }, { closeTimeout: 2_000 }, { retryInitial: 60_000, retryMax: 60_000 });
    await h.client.ready(2_000);
    p.eventsReplies.push({ kind: "status", status: 500 });
    h.client.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    await h.client.flush();
    await eventually(() => {
      expect(p.batches).toHaveLength(1);
    });

    // The retry is a minute out; close does not wait for it.
    const began = Date.now();
    await h.client.close();
    expect(Date.now() - began).toBeLessThan(2_000);
    expect(p.batches).toHaveLength(2);
    expect(p.batches[1]?.eventIds).toStrictEqual(p.batches[0]?.eventIds);
  });
});
