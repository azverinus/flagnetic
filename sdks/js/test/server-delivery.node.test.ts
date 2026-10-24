// Server mode against a mock platform on Node's http server: the mock
// scenarios for a server SDK, each asserting values, stage
// transitions, listener calls and log events. Timings are scaled down
// through the client's test seam; the behaviour is the production one.
import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, test } from "vitest";
import type { Timing } from "../src/server/delivery.js";
import { createServerWith } from "../src/server/client.js";
import type { FlagneticServer, LogEvent, LogLevel, ServerOptions, Stage } from "../src/server.js";
import { MockPlatform, rulesetOf } from "./support/mock-platform.js";

const key = "fn_srv_0123456789abcdefghijklmnopqrstuvwxyzABCD";

const fast: Timing = {
  requestTimeout: 300,
  pollConnected: 10_000,
  pollDisconnected: 10_000,
  retryInitial: 40,
  retryMax: 80,
  unauthorizedMax: 160,
  streamRetryInitial: 40,
  streamRetryMax: 200,
  streamStable: 1_000,
  streamSilence: 10_000,
  streamCloseJitter: 50,
  catchUp: [40, 80, 160],
  streamFirstWait: 500,
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
  stages: Stage[];
  changes: string[][];
}

const cleanups: (() => Promise<void>)[] = [];
afterEach(async () => {
  for (const c of cleanups.splice(0).reverse()) await c();
});

async function platform(document?: Record<string, unknown>): Promise<MockPlatform> {
  const p = await MockPlatform.start(document);
  cleanups.push(() => p.stop());
  return p;
}

function start(
  p: MockPlatform,
  options: ServerOptions = {},
  timing: Partial<Timing> = {},
  random: () => number = Math.random,
): Harness {
  const logs: Logged[] = [];
  const client = createServerWith(key, p.baseUrl, { logger: (level, event, fields) => logs.push({ level, event, fields }), ...options }, {
    timing: { ...fast, ...timing },
    random,
  });
  cleanups.push(() => client.close());
  const stages: Stage[] = [];
  const changes: string[][] = [];
  client.onStage((s) => stages.push(s));
  client.onChange((k) => changes.push(k));
  return { client, logs, events: (name) => logs.filter((l) => l.event === name), stages, changes };
}

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

const ctx = { units: { user: "u-1" } };

// failures returns the fields of every fetch_failed event but retry_in_ms,
// after checking that each names when the next attempt is due.
function failures(h: Harness): Record<string, unknown>[] {
  return h.events("fetch_failed").map((l) => {
    const { retry_in_ms: due, ...rest } = l.fields;
    expect(due).toBeTypeOf("number");
    return rest;
  });
}

// gapsOf returns the differences between consecutive times.
function gapsOf(times: number[]): number[] {
  return times.slice(1).map((t, i) => t - (times[i] ?? NaN));
}

// expectGaps checks the gaps between consecutive request times against the
// delays expected, allowing for timer and network latency but not for the
// next step of the backoff.
function expectGaps(times: number[], expected: number[]): void {
  const gaps = gapsOf(times).slice(0, expected.length);
  expect(gaps).toHaveLength(expected.length);
  gaps.forEach((g, i) => {
    const want = expected[i] ?? NaN;
    expect(g, `gap ${i} of ${JSON.stringify(gaps)}`).toBeGreaterThanOrEqual(want - 10);
    expect(g, `gap ${i} of ${JSON.stringify(gaps)}`).toBeLessThan(want + 80);
  });
}

describe("the ruleset fetch", () => {
  test("opens the stream before fetching and sends the identity, schema and gzip headers and If-None-Match on the next fetch", async () => {
    const p = await platform(rulesetOf(1, { a: true }));
    p.gzip = true;
    const h = start(p, {}, { pollConnected: 100 });
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    expect(h.client.getBool(ctx, "a", false)).toBe(true);
    expect(p.requests[0]?.path).toBe("/sdk/v1/stream");

    const [first] = p.rulesetRequests();
    expect(first?.headers).toMatchObject({
      authorization: `Bearer ${key}`,
      "user-agent": "flagnetic-js/0.0.0 (node)",
      "flagnetic-schema-version": "1",
      accept: "application/json",
    });
    expect(first?.headers["accept-encoding"]).toMatch(/gzip/);
    expect(first?.headers["if-none-match"]).toBeUndefined();
    const stream = p.streamRequests()[0];
    expect(stream?.headers).toMatchObject({ authorization: `Bearer ${key}`, accept: "text/event-stream" });
    expect(stream?.headers["last-event-id"]).toBeUndefined();

    await p.until(() => p.rulesetRequests().length >= 2);
    expect(p.rulesetRequests()[1]?.headers["if-none-match"]).toBe('"1"');
    expect(h.events("ruleset_loaded").map((l) => l.fields)).toStrictEqual([{ source: "network", ruleset_version: 1, flags: 1 }]);
    expect(h.events("sse_connected")).toHaveLength(1);
    expect(h.changes).toStrictEqual([["a"]]);
  });

  test("the platform option names the platform in the User-Agent", async () => {
    const p = await platform(rulesetOf(1, {}));
    const h = start(p, { platform: "linux", stream: false });
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    expect(p.rulesetRequests()[0]?.headers["user-agent"]).toBe("flagnetic-js/0.0.0 (linux)");
  });

  test("the initial fetch times out, ready resolves built_in at its timeout, a later 200 moves to fresh", async () => {
    const p = await platform(rulesetOf(1, { a: true }));
    p.rulesetReplies.push({ kind: "hang" });
    const h = start(p, { stream: false });
    const began = Date.now();
    await expect(h.client.ready(100)).resolves.toBe("built_in");
    expect(Date.now() - began).toBeLessThan(250);
    expect(h.client.getDetail(ctx, "a", false)).toMatchObject({ value: false, reason: "fallback", errorCode: "no_ruleset", stage: "built_in" });
    await expect(h.client.ready()).resolves.toBe("fresh");
    expect(h.client.getBool(ctx, "a", false)).toBe(true);
    expect(h.stages).toStrictEqual(["fresh"]);
    expect(failures(h)).toStrictEqual([{ cause: "timed out after 300 ms" }]);
  });

  test("a 500 after a good ruleset keeps the values at last_good, backs off, and recovers on the next 200", async () => {
    const p = await platform(rulesetOf(1, { a: true }));
    const h = start(p, { stream: false }, { pollDisconnected: 30 });
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    p.rulesetReplies.push({ kind: "status", status: 500 }, { kind: "status", status: 503 });
    await eventually(() => {
      expect(h.stages).toStrictEqual(["fresh", "last_good", "fresh"]);
    });
    expect(h.client.getDetail(ctx, "a", false)).toMatchObject({ value: true, reason: "fallthrough", stage: "fresh" });
    expect(failures(h)).toStrictEqual([
      { cause: "status 500", status: 500 },
      { cause: "status 503", status: 503 },
    ]);
    expect(h.events("stage_changed").map((l) => [l.level, l.fields])).toStrictEqual([
      ["info", { from: "built_in", to: "fresh" }],
      ["warn", { from: "fresh", to: "last_good" }],
      ["info", { from: "last_good", to: "fresh" }],
    ]);
    // The recovery was a 304: If-None-Match with the held version.
    expect(p.rulesetRequests().at(-1)?.headers["if-none-match"]).toBe('"1"');
  });

  test("while stage last_good, a detail reports it with the held ruleset's version and age", async () => {
    const p = await platform(rulesetOf(4, { a: true }));
    const h = start(p, { stream: false }, { pollDisconnected: 20, retryInitial: 5_000, retryMax: 5_000 });
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    p.rulesetDefault = { kind: "status", status: 502 };
    await eventually(() => {
      expect(h.client.stage).toBe("last_good");
    });
    const d = h.client.getDetail(ctx, "a", false);
    expect(d).toMatchObject({ value: true, stage: "last_good", rulesetVersion: 4 });
    expect(Date.now() - (d.receivedAt?.getTime() ?? 0)).toBeLessThan(2000);
  });

  test("backoff doubles from the initial delay with full jitter up to its cap", async () => {
    const p = await platform(rulesetOf(1, {}));
    p.rulesetDefault = { kind: "status", status: 500 };
    // random() = 1 draws the top of each jitter range: 100, 200, then the 200 cap.
    start(p, { stream: false }, { retryInitial: 100, retryMax: 200 }, () => 1);
    await p.until(() => p.rulesetRequests().length >= 5);
    expectGaps(p.rulesetRequests().map((r) => r.at), [100, 200, 200, 200]);

    // random() = 0 is the bottom of the range: an immediate retry.
    const q = await platform(rulesetOf(1, {}));
    q.rulesetDefault = { kind: "status", status: 500 };
    start(q, { stream: false }, { retryInitial: 5_000, retryMax: 5_000 }, () => 0);
    await q.until(() => q.rulesetRequests().length >= 3, 1000);
  });

  test("a 429 waits at least its Retry-After", async () => {
    const p = await platform(rulesetOf(1, {}));
    p.rulesetReplies.push({ kind: "status", status: 429, headers: { "Retry-After": "0.3" } });
    const h = start(p, { stream: false }, {}, () => 0);
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    expect(gapsOf(p.rulesetRequests().map((r) => r.at))[0]).toBeGreaterThanOrEqual(290);
  });

  test("a Retry-After beyond what a timer holds still waits instead of retrying at once", async () => {
    const p = await platform(rulesetOf(1, {}));
    p.rulesetReplies.push({ kind: "status", status: 429, headers: { "Retry-After": "99999999" } });
    start(p, { stream: false }, {}, () => 0);
    await p.until(() => p.rulesetRequests().length >= 1);
    await new Promise((resolve) => setTimeout(resolve, 300));
    expect(p.rulesetRequests()).toHaveLength(1);
  });

  test.each([
    ["corrupt JSON", `{"schema_version":1,`, "parse: malformed JSON"],
    ["an empty body", "", "parse: empty body"],
    ["{}", "{}", "parse: decode ruleset: unsupported schema_version 0, this evaluator reads 1"],
    ["a missing schema_version", `{"version":2,"scope":"server","flags":[]}`, "parse: decode ruleset: unsupported schema_version 0, this evaluator reads 1"],
    ["the wrong document shape", `{"schema_version":1,"version":2,"scope":"server","flags":{}}`, "parse: decode ruleset: flags must be an array"],
    ["a client-scope document", `{"schema_version":1,"version":2,"scope":"client","flags":[]}`, 'parse: decode ruleset: unknown scope "client"'],
    ["a lower version", `{"schema_version":1,"version":2,"scope":"server","flags":[]}`, "version 2 below the held 3"],
  ])("%s is a failure: fetch_failed with its cause, last good kept", async (_, body, cause) => {
    const p = await platform(rulesetOf(3, { a: true }));
    const h = start(p, { stream: false }, { pollDisconnected: 20, retryInitial: 5_000, retryMax: 5_000 });
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    p.rulesetReplies.push({ kind: "body", body });
    await eventually(() => {
      expect(h.client.stage).toBe("last_good");
    });
    expect(failures(h)).toStrictEqual([{ cause }]);
    expect(h.client.getDetail(ctx, "a", false)).toMatchObject({ value: true, rulesetVersion: 3, stage: "last_good" });
    expect(h.changes).toStrictEqual([["a"]]);
  });

  test("an empty ruleset is fresh and every getter serves its built-in default", async () => {
    const p = await platform(rulesetOf(1, {}));
    const h = start(p, { stream: false });
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    expect(h.client.getDetail(ctx, "a", true)).toMatchObject({ value: true, reason: "fallback", errorCode: "flag_not_found", stage: "fresh" });
  });

  test("a revoked key logs key_revoked once at error, closes the stream, retries at the unauthorized cap and keeps serving", async () => {
    const p = await platform(rulesetOf(1, { a: true }));
    const h = start(p, {}, { pollConnected: 20 }, () => 1);
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    await p.until(() => p.openStreams === 1);
    p.rulesetDefault = { kind: "status", status: 401 };
    p.streamDefault = { kind: "status", status: 401 };
    await p.until(() => p.rulesetRequests().filter((r) => r.at > 0).length >= 7, 3000);
    expect(p.openStreams).toBe(0);
    const revoked = h.events("key_revoked");
    expect(revoked).toHaveLength(1);
    expect(revoked[0]).toMatchObject({ level: "error", fields: { status: 401 } });
    expect(h.client.getDetail(ctx, "a", false)).toMatchObject({ value: true, stage: "last_good" });
    // The fourth and later failures wait the unauthorized cap, above the ordinary one.
    expect(gapsOf(p.rulesetRequests().map((r) => r.at)).at(-1)).toBeGreaterThanOrEqual(150);

    // The key is restored: the next fetch recovers and the stream comes back.
    p.rulesetDefault = { kind: "current" };
    p.streamDefault = { kind: "open" };
    await eventually(() => {
      expect(h.client.stage).toBe("fresh");
      expect(p.openStreams).toBe(1);
    });
  });

  test("an unsupported schema_version logs schema_unsupported once and keeps the last good", async () => {
    const p = await platform(rulesetOf(1, { a: true }));
    const h = start(p, { stream: false }, { pollDisconnected: 20 }, () => 0);
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    const future = JSON.stringify({ ...rulesetOf(2, { a: false }), schema_version: 2 });
    p.rulesetReplies.push({ kind: "body", body: future }, { kind: "body", body: future });
    await p.until(() => p.rulesetRequests().length >= 4);
    expect(h.events("schema_unsupported").map((l) => [l.level, l.fields])).toStrictEqual([["error", { schema_version: 2, supported: [1] }]]);
    expect(h.events("fetch_failed").map((l) => l.fields.cause)).toStrictEqual(["schema_version 2 unsupported", "schema_version 2 unsupported"]);
    expect(h.client.getBool(ctx, "a", false)).toBe(true);
  });

  test("with the stream disabled the client polls at the short interval; with it connected it does not", async () => {
    const p = await platform(rulesetOf(1, {}));
    start(p, { stream: false }, { pollDisconnected: 50, pollConnected: 10_000 });
    await sleep(300);
    expect(p.rulesetRequests().length).toBeGreaterThanOrEqual(4);
    expect(p.streamRequests()).toHaveLength(0);

    const q = await platform(rulesetOf(1, {}));
    start(q, {}, { pollDisconnected: 50, pollConnected: 10_000 });
    await sleep(300);
    expect(q.rulesetRequests()).toHaveLength(1);
  });

  test("close returns promptly during backoff and stops every request", async () => {
    const p = await platform(rulesetOf(1, {}));
    p.rulesetDefault = { kind: "status", status: 500 };
    const h = start(p, {}, { retryInitial: 60_000, retryMax: 60_000 }, () => 1);
    await p.until(() => p.rulesetRequests().length === 1);
    const began = Date.now();
    await h.client.close();
    expect(Date.now() - began).toBeLessThan(50);
    const seen = p.requests.length;
    await sleep(150);
    expect(p.requests.length).toBe(seen);
    await expect(h.client.ready(10_000)).resolves.toBe("built_in");
    await expect(h.client.flush()).resolves.toBeUndefined();
  });
});

describe("the ruleset stream", () => {
  test("an event reaches onChange with exactly the changed keys within 2 s", async () => {
    const p = await platform(rulesetOf(1, { a: true, b: true }));
    const h = start(p);
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    await p.until(() => p.openStreams === 1);
    h.changes.length = 0;
    const began = Date.now();
    p.publish(rulesetOf(2, { a: false, b: true }));
    await eventually(() => {
      expect(h.changes).toStrictEqual([["a"]]);
    }, 2000);
    const elapsed = Date.now() - began;
    expect(elapsed).toBeLessThan(2000);
    // Unconditional, not a test reporter line: CI puts the kill-switch
    // propagation number in the job summary (the 2 s target of a kill
    // switch).
    console.log(`kill_switch_propagation_ms=${elapsed}`);
    expect(h.client.getBool(ctx, "a", true)).toBe(false);
    expect(p.rulesetRequests().at(-1)?.headers["if-none-match"]).toBe('"1"');
  });

  test("an event at or below the held version fetches nothing; a 304 fires no change", async () => {
    const p = await platform(rulesetOf(2, { a: true }));
    const h = start(p);
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    await p.until(() => p.openStreams === 1 && p.rulesetRequests().length === 1);
    p.emit(2);
    p.emit(1);
    await sleep(100);
    expect(p.rulesetRequests()).toHaveLength(1);
    expect(h.changes).toStrictEqual([["a"]]);
  });

  test("the server closes the stream, the SDK reconnects with Last-Event-ID, refetches and picks up a version bumped in the gap", async () => {
    const p = await platform(rulesetOf(1, { a: true }));
    const h = start(p);
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    await p.until(() => p.openStreams === 1);
    h.changes.length = 0;
    p.streamReplies.push({ kind: "status", status: 502 });
    p.closeStreams();
    p.setDocument(rulesetOf(2, { a: false }));
    await eventually(() => {
      expect(h.changes).toStrictEqual([["a"]]);
    });
    const streams = p.streamRequests();
    expect(streams.length).toBeGreaterThanOrEqual(3);
    expect(streams.at(-1)?.headers["last-event-id"]).toBe("1");
    expect(h.events("sse_dropped").map((l) => l.fields.cause)).toStrictEqual(["closed by the server", "status 502 application/problem+json"]);
    expect(h.events("sse_connected")).toHaveLength(2);
    expect(h.client.getBool(ctx, "a", true)).toBe(false);
  });

  test("a reconnect is always followed by a fetch", async () => {
    const p = await platform(rulesetOf(1, { a: true }));
    const h = start(p);
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    await p.until(() => p.openStreams === 1);
    const fetches = p.rulesetRequests().length;
    p.closeStreams();
    await p.until(() => p.streamRequests().length === 2 && p.rulesetRequests().length === fetches + 1);
    expect(h.client.stage).toBe("fresh");
  });

  test("45 s of silence (scaled) reconnects; heartbeats keep the stream open", async () => {
    const p = await platform(rulesetOf(1, {}));
    const h = start(p, {}, { streamSilence: 150 });
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    const beat = setInterval(() => { p.write(": hb\n\n"); }, 40);
    await sleep(400);
    clearInterval(beat);
    expect(p.streamRequests()).toHaveLength(1);
    await p.until(() => p.streamRequests().length >= 2, 1000);
    expect(h.events("sse_dropped")[0]?.fields.cause).toBe("no frame for 150 ms");
  });

  test("a fetch below the announced version is retried after 1, 2 and 4 s (scaled); the version never goes down", async () => {
    const p = await platform(rulesetOf(2, { a: true }));
    const h = start(p, {}, { catchUp: [100, 200, 400] });
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    await p.until(() => p.openStreams === 1 && p.rulesetRequests().length === 1);
    // A replica behind: the stream announces 3, the ruleset endpoint still serves 2.
    p.emit(3);
    await p.until(() => p.rulesetRequests().length === 5);
    expectGaps(p.rulesetRequests().map((r) => r.at).slice(1), [100, 200, 400]);
    await sleep(250);
    expect(p.rulesetRequests()).toHaveLength(5);
    expect(h.client.stage).toBe("fresh");

    // A lower version is refused and the held one stays.
    p.rulesetReplies.push({ kind: "body", body: JSON.stringify(rulesetOf(1, { a: false })) });
    p.emit(4);
    await eventually(() => {
      expect(h.events("fetch_failed").map((l) => l.fields.cause)).toContain("version 1 below the held 2");
    });
    expect(h.client.getDetail(ctx, "a", false)).toMatchObject({ value: true, rulesetVersion: 2 });
  });

  test("a lower version answering a streamed one is retried on the 1, 2, 4 s sequence (scaled), once", async () => {
    const p = await platform(rulesetOf(2, { a: true }));
    const h = start(p, {}, { catchUp: [100, 200, 400], retryInitial: 5_000, retryMax: 5_000 });
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    await p.until(() => p.openStreams === 1 && p.rulesetRequests().length === 1);
    p.rulesetDefault = { kind: "body", body: JSON.stringify(rulesetOf(1, { a: false })) };
    p.emit(3);
    await p.until(() => p.rulesetRequests().length === 5);
    expectGaps(p.rulesetRequests().map((r) => r.at).slice(1), [100, 200, 400]);
    await new Promise((r) => setTimeout(r, 300));
    expect(p.rulesetRequests()).toHaveLength(5);
    expect(h.client.getDetail(ctx, "a", false)).toMatchObject({ value: true, rulesetVersion: 2, stage: "last_good" });
  });

  test("the parser reads CRLF lines, multi-line data, comments and ids", async () => {
    const p = await platform(rulesetOf(1, { a: true }));
    const h = start(p);
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    await p.until(() => p.openStreams === 1);
    p.setDocument(rulesetOf(5, { a: false }));
    p.write(': comment\r\nevent: other\r\ndata: {"version":9}\r\n\r\n');
    p.write('event: ruleset\r\nid: 5\r\ndata: {"version"\r\ndata: :5}\r\n\r');
    p.write("\n");
    await eventually(() => {
      expect(h.client.getBool(ctx, "a", true)).toBe(false);
    });
  });

  test("a 401 on the stream logs key_revoked and does not reconnect", async () => {
    const p = await platform(rulesetOf(1, {}));
    p.streamDefault = { kind: "status", status: 403 };
    const h = start(p);
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    await sleep(200);
    expect(p.streamRequests()).toHaveLength(1);
    expect(h.events("key_revoked").map((l) => l.fields)).toStrictEqual([{ status: 403 }]);
  });

  test("a failing stream reconnects with backoff while polling takes over at the short interval", async () => {
    const p = await platform(rulesetOf(1, {}));
    p.streamDefault = { kind: "status", status: 500 };
    const h = start(p, {}, { pollDisconnected: 60, streamRetryInitial: 100, streamRetryMax: 1000 }, () => 1);
    await expect(h.client.ready(2000)).resolves.toBe("fresh");
    await p.until(() => p.streamRequests().length >= 4, 2000);
    expectGaps(p.streamRequests().map((r) => r.at), [100, 200, 400]);
    expect(p.rulesetRequests().length).toBeGreaterThanOrEqual(4);
    expect(h.events("sse_dropped")[0]?.fields).toMatchObject({ cause: "status 500 application/problem+json", retry_in_ms: 100 });
  });
});

describe("the store", () => {
  test("persists every accepted ruleset and loads it as last_good before any network call", async () => {
    const dir = await mkdtemp(join(tmpdir(), "flagnetic-"));
    const store = join(dir, "ruleset.json");
    const p = await platform(rulesetOf(3, { a: true }));
    const first = start(p, { store, stream: false });
    await first.client.ready(2000);
    await first.client.close();
    await eventually(async () => {
      expect(JSON.parse(await readFile(store, "utf8"))).toMatchObject({ version: 3 });
    });

    const down = await platform();
    down.rulesetDefault = { kind: "status", status: 500 };
    const second = start(down, { store, stream: false }, { retryInitial: 5_000, retryMax: 5_000 });
    await eventually(() => {
      expect(second.client.stage).toBe("last_good");
    });
    expect(second.client.getDetail(ctx, "a", false)).toMatchObject({ value: true, stage: "last_good", rulesetVersion: 3 });
    expect(second.events("ruleset_loaded")[0]?.fields).toMatchObject({ source: "store" });
    expect(second.changes).toStrictEqual([["a"]]);
    await down.until(() => down.rulesetRequests().length === 1);
    expect(down.rulesetRequests()[0]?.headers["if-none-match"]).toBe('"3"');
  });

  test("a corrupt store file yields built_in and store_failed", async () => {
    const dir = await mkdtemp(join(tmpdir(), "flagnetic-"));
    const store = join(dir, "ruleset.json");
    await writeFile(store, "{not json");
    const down = await platform();
    down.rulesetDefault = { kind: "status", status: 500 };
    const h = start(down, { store, stream: false }, { retryInitial: 5_000, retryMax: 5_000 });
    await down.until(() => down.rulesetRequests().length === 1);
    expect(h.client.stage).toBe("built_in");
    expect(h.events("store_failed")[0]?.fields).toMatchObject({ operation: "read" });
  });
});
