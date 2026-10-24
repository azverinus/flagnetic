// Runs the protocol fixtures of conformance/protocol against server mode:
// the server-mode scenarios of scenarios.json and the ruleset degradation
// cases of degradation/cases.json, scripted on a mock server. Time is
// virtual: the client schedules on a clock the runner advances, so a
// window of 30 s is checked exactly and costs nothing; HTTP is real.
import { mkdtemp, writeFile } from "node:fs/promises";
import { readFileSync } from "node:fs";
import { createServer as createHttpServer, type IncomingMessage, type ServerResponse } from "node:http";
import { gunzipSync } from "node:zlib";
import type { AddressInfo } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, test } from "vitest";
import { createServerWith } from "../src/server/client.js";
import { VirtualClock } from "./support/virtual-clock.js";
import type { TraceEvent } from "../src/server/delivery.js";
import { LocalOverrideRefused, LocalOverridesNotAllowed, testServer } from "../src/server.js";
import type { Context, Experiment, FlagneticServer, JsonValue, LogEvent, Recorder, ServerOptions } from "../src/server.js";

const root = fileURLToPath(new URL("../../../conformance/protocol/", import.meta.url));
const read = (path: string): string => readFileSync(join(root, path), "utf8");
const key = "fn_srv_0123456789abcdefghijklmnopqrstuvwxyzABCD";

// ---------------------------------------------------------------- fixtures

interface Answer {
  readonly flag: string;
  readonly type: "boolean" | "string" | "number" | "json";
  readonly default: JsonValue;
  readonly value: JsonValue;
  readonly variation?: string;
  readonly reason: string;
  /** The experiment block of a value that came from a running experiment, as the fixtures spell it. */
  readonly experiment?: Record<string, JsonValue>;
}

interface Respond {
  readonly status?: number;
  readonly headers?: Record<string, string>;
  readonly body?: string;
  readonly network_error?: boolean;
  readonly stream?: boolean;
}

/** What a scenario expects of one ingest batch. */
interface EventsExpectation {
  readonly types?: string[];
  readonly same_ids_as_step?: number;
  readonly excludes_ids_of_step?: number;
  readonly summary_counts?: { flag: string; variation: string | null; reason: string; count: number }[];
  readonly summary_ruleset_version?: number;
  readonly absent_strings?: string[];
  /** The fields each exposure event of the batch must carry, in order. */
  readonly exposures?: Record<string, JsonValue>[];
}

interface Step {
  readonly note?: string;
  readonly request?: {
    readonly method: string;
    readonly path: string;
    readonly headers?: Record<string, string>;
    readonly absent_headers?: string[];
    readonly delay_ms?: { readonly min: number; readonly max: number };
    readonly events?: EventsExpectation;
  };
  readonly respond?: Respond;
  readonly stream_send?: string;
  readonly stream_close?: boolean;
  readonly advance_ms?: number;
  readonly no_request?: { readonly path?: string; readonly for_ms: number };
  readonly call?: { readonly op: string; readonly flag?: string; readonly type?: Answer["type"]; readonly default?: JsonValue; readonly value?: JsonValue; readonly times?: number };
  readonly expect?: {
    readonly stage?: string;
    readonly stale_since?: "set" | "unset";
    readonly answers_of?: string;
    readonly answers?: readonly Answer[];
    readonly logs?: Record<string, number>;
    readonly result?: Answer;
    readonly refused?: boolean;
    readonly recorded?: unknown;
  };
}

interface Scenario {
  readonly name: string;
  readonly mode: "server" | "client";
  readonly given: { readonly stored?: string; readonly offline?: string; readonly options: Record<string, unknown> };
  readonly steps: readonly Step[];
}

const scenarios = JSON.parse(read("scenarios.json")) as {
  context: Context;
  answers: Record<string, Answer[]>;
  scenarios: Scenario[];
};
// What only the SDKs decide (versioning and compatibility), in the same step
// language over the same server-produced documents.
scenarios.scenarios.push(...(JSON.parse(read("versioning-scenarios.json")) as { scenarios: Scenario[] }).scenarios);
// Running experiments: their own answers join those of the scenarios above.
const experiments = JSON.parse(read("experiment-scenarios.json")) as { answers: Record<string, Answer[]>; scenarios: Scenario[] };
Object.assign(scenarios.answers, experiments.answers);
scenarios.scenarios.push(...experiments.scenarios);

const cases = JSON.parse(read("degradation/cases.json")) as {
  context: Context;
  cases: { name: string; endpoint: string; held: string; response: Respond; outcome: string; answers: Answer[]; logs?: Record<string, number> }[];
};

// ---------------------------------------------------------------- the runner

interface Arrived {
  readonly method: string;
  readonly path: string;
  readonly headers: IncomingMessage["headers"];
  readonly at: number;
  readonly res: ServerResponse;
  /** The request's body, decompressed, once it has all arrived. */
  readonly body: Promise<string>;
}

/** One row of an evaluation summary, as a scenario writes it. */
interface Count {
  readonly flag: string;
  readonly variation: string | null;
  readonly reason: string;
  readonly count: number;
}

function need<T>(v: T | undefined): T {
  if (v === undefined) throw new Error("the scenario step lacks a field its call needs");
  return v;
}

const realSleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

/**
 * Lets the promises the client is waiting on run to completion: a request
 * the SDK starts is several microtasks away from reaching the socket.
 */
async function settle(): Promise<void> {
  for (let i = 0; i < 8; i++) await realSleep(0);
}

async function until(predicate: () => boolean, what: string, timeout = 5000): Promise<void> {
  const deadline = Date.now() + timeout;
  while (!predicate()) {
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await realSleep(1);
  }
}

/** An experiment block in the spelling of the protocol's documents. */
function experimentFields(e: Experiment): Record<string, JsonValue> {
  return {
    key: e.key,
    variation: e.variation,
    unit_type: e.unitType,
    unit_id: e.unitId,
    inclusion_salt: e.inclusionSalt,
    variation_salt: e.variationSalt,
    inclusion_bucket: e.inclusionBucket,
    variation_bucket: e.variationBucket,
    override: e.override,
  };
}

class Runner {
  readonly clock = new VirtualClock();
  readonly queue: Arrived[] = [];
  readonly logs: LogEvent[] = [];
  client!: FlagneticServer;
  /** What an offline client would have reported; undefined online. */
  recorder: Recorder | undefined;
  private started = 0;
  private arrived = 0;
  private handled = { fetch: 0, events: 0, opened: 0, ended: 0, bytes: 0 };
  private sentBytes = 0;
  private stream: ServerResponse | undefined;
  /** The event IDs of the batch each step's request carried. */
  private readonly batches = new Map<number, string[]>();
  /** The evaluations this runner made while looking at answers, to discount from a summary. */
  private readonly observed = new Map<string, number>();
  private step = 0;
  private readonly server = createHttpServer((req, res) => {
    const path = new URL(req.url ?? "/", "http://x").pathname;
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    const body = new Promise<string>((resolve) => {
      req.on("end", () => {
        const raw = Buffer.concat(chunks);
        resolve((/\bgzip\b/.test(req.headers["content-encoding"] ?? "") ? gunzipSync(raw) : raw).toString("utf8"));
      });
    });
    this.queue.push({ method: req.method ?? "", path, headers: req.headers, at: this.clock.now(), res, body });
    this.arrived++;
  });
  private baseUrl = "";

  constructor(private readonly context: Context) {}

  async start(given: Scenario["given"]): Promise<void> {
    await new Promise<void>((resolve) => this.server.listen(0, "127.0.0.1", resolve));
    this.baseUrl = `http://127.0.0.1:${(this.server.address() as AddressInfo).port}`;
    const o = given.options;
    const options: ServerOptions = {
      stream: o.sse === true,
      pollInterval: o.poll_interval_ms as number,
      pollIntervalWithoutStream: o.poll_interval_ms as number,
      allowOverrides: o.allow_overrides === true,
      sendSummaries: o.send_summaries !== false,
      sendExposures: o.send_exposures !== false,
      logger: (_level, event) => this.logs.push(event),
    };
    const eventTiming = {
      ...(typeof o.flush_interval_ms === "number" && { flushInterval: o.flush_interval_ms }),
      ...(typeof o.summary_interval_ms === "number" && { summaryInterval: o.summary_interval_ms }),
    };
    let store: string | undefined;
    if (given.stored !== undefined) {
      store = join(await mkdtemp(join(tmpdir(), "flagnetic-protocol-")), "ruleset.json");
      await writeFile(store, read(given.stored));
    }
    if (given.offline !== undefined) {
      // Offline makes no request and schedules nothing, so the public
      // test helper stands for the client.
      const offline = testServer(read(given.offline), options);
      this.client = offline;
      this.recorder = offline.recorder;
      return;
    }
    this.client = createServerWith(
      key,
      this.baseUrl,
      {
        ...options,
        ...(store !== undefined && { store }),
      },
      {
        clock: this.clock,
        eventTiming,
        fetch: (input, init) => {
          this.started++;
          return fetch(input, init);
        },
        trace: (e: TraceEvent) => {
          if (e.kind === "fetch_handled") this.handled.fetch++;
          else if (e.kind === "events_handled") this.handled.events++;
          else if (e.kind === "stream_opened") this.handled.opened++;
          else if (e.kind === "stream_ended") this.handled.ended++;
          else this.handled.bytes += e.bytes;
        },
      },
    );
    // Construction settles: a store is read before the first request.
    const deadline = Date.now() + 200;
    while (this.started === 0 && Date.now() < deadline) await realSleep(2);
    await this.quiet();
  }

  async stop(): Promise<void> {
    // The server goes first: close flushes what is queued, and a request to
    // a socket nobody answers would wait on a clock this runner drives.
    for (const r of this.queue) r.res.destroy();
    this.stream?.destroy();
    this.server.closeAllConnections();
    await new Promise<void>((resolve) => this.server.close(() => { resolve(); }));
    await this.client.close();
  }

  /** Waits until every request the client started has reached the server. */
  private async quiet(): Promise<void> {
    await settle();
    await until(() => this.arrived >= this.started, "requests in flight to arrive");
  }

  async run(steps: readonly Step[]): Promise<void> {
    let stepStart = this.clock.now();
    for (const [i, step] of steps.entries()) {
      this.step = i;
      const where = `step ${i}${step.note ? ` (${step.note})` : ""}`;
      if (step.request) await this.request(step.request, step.respond ?? { status: 200 }, stepStart, where);
      if (step.stream_send !== undefined) await this.send(step.stream_send);
      if (step.stream_close) await this.closeStream();
      if (step.advance_ms !== undefined) await this.advance(this.clock.now() + step.advance_ms, undefined, where);
      if (step.no_request) await this.advance(this.clock.now() + step.no_request.for_ms, step.no_request.path ?? "*", where);
      const result = step.call ? await this.call(step.call) : undefined;
      if (step.expect) this.check(step.expect, result, where);
      stepStart = this.clock.now();
    }
    // After the last step no request may be left unanswered or unexpected.
    await this.quiet();
    expect(this.queue.map((r) => `${r.method} ${r.path}`), "requests after the last step").toStrictEqual([]);
  }

  private async request(want: NonNullable<Step["request"]>, respond: Respond, stepStart: number, where: string): Promise<void> {
    const window = want.delay_ms ?? { min: 0, max: 0 };
    let req: Arrived | undefined;
    for (;;) {
      await this.quiet();
      const at = this.queue.findIndex((r) => r.path === want.path);
      if (at >= 0) {
        [req] = this.queue.splice(at, 1);
        break;
      }
      const next = this.clock.next();
      if (next === undefined || next - stepStart > window.max) {
        throw new Error(`${where}: no ${want.path} request within ${window.max} ms (next timer at +${next === undefined ? "never" : next - stepStart})`);
      }
      this.clock.fireNext();
    }
    if (!req) throw new Error(`${where}: lost the ${want.path} request`);
    const elapsed = req.at - stepStart;
    expect(elapsed, `${where}: delay of ${want.path}`).toBeGreaterThanOrEqual(window.min);
    expect(elapsed, `${where}: delay of ${want.path}`).toBeLessThanOrEqual(window.max);
    expect(req.method, where).toBe(want.method);
    for (const [name, value] of Object.entries(want.headers ?? {})) {
      expect(req.headers[name.toLowerCase()], `${where}: header ${name}`).toBe(value.replace("<sdk key>", key));
    }
    for (const name of want.absent_headers ?? []) {
      expect(req.headers[name.toLowerCase()], `${where}: header ${name} absent`).toBeUndefined();
    }
    if (want.events) this.batches.set(this.step, await this.checkEvents(want.events, req, where));
    await this.respond(req, respond);
  }

  private async checkEvents(want: EventsExpectation, req: Arrived, where: string): Promise<string[]> {
    const text = await req.body;
    const body = JSON.parse(text) as {
      schema_version: number;
      sdk: { name: string; version: string; platform: string };
      sent_at: string;
      events: Record<string, unknown>[];
    };
    expect(body.schema_version, `${where}: envelope schema_version`).toBe(1);
    expect(body.sdk.name, `${where}: envelope sdk name`).toBe("flagnetic-js");
    const ids = body.events.map((e) => e.event_id as string);
    if (want.types) expect(body.events.map((e) => e.type), `${where}: event types`).toStrictEqual(want.types);
    if (want.same_ids_as_step !== undefined) {
      expect(ids, `${where}: the same event IDs as step ${want.same_ids_as_step}`).toStrictEqual(this.batches.get(want.same_ids_as_step));
    }
    if (want.excludes_ids_of_step !== undefined) {
      const before = this.batches.get(want.excludes_ids_of_step) ?? [];
      expect(ids.filter((id) => before.includes(id)), `${where}: no event IDs of step ${want.excludes_ids_of_step}`).toStrictEqual([]);
    }
    if (want.summary_counts) {
      const summary = body.events.find((e) => e.type === "evaluation_summary");
      expect(summary, `${where}: an evaluation summary`).toBeDefined();
      const counts = this.discountObserved(summary?.counts as Count[]);
      const order = (rows: Count[]): Count[] =>
        [...rows].sort((a, b) => `${a.flag}${a.variation ?? ""}${a.reason}`.localeCompare(`${b.flag}${b.variation ?? ""}${b.reason}`));
      expect(order(counts), `${where}: summary counts`).toStrictEqual(order(want.summary_counts));
      if (want.summary_ruleset_version !== undefined) {
        expect(summary?.ruleset_version, `${where}: summary ruleset_version`).toBe(want.summary_ruleset_version);
      }
    }
    if (want.exposures) {
      const exposures = body.events.filter((e) => e.type === "exposure");
      expect(exposures, `${where}: exposures`).toHaveLength(want.exposures.length);
      want.exposures.forEach((fields, i) => {
        expect(exposures[i], `${where}: exposure ${i}`).toMatchObject(fields);
      });
    }
    for (const absent of want.absent_strings ?? []) {
      expect(text.includes(absent), `${where}: ${JSON.stringify(absent)} is nowhere in the body`).toBe(false);
    }
    return ids;
  }

  // Subtracts the evaluations this runner made while checking answers: the
  // scenario's `answers` are observations, its `call` steps are what the
  // counts are about.
  private discountObserved(counts: Count[]): Count[] {
    const out: Count[] = [];
    for (const row of counts) {
      const id = JSON.stringify([row.flag, row.variation, row.reason]);
      const mine = Math.min(this.observed.get(id) ?? 0, row.count);
      this.observed.set(id, (this.observed.get(id) ?? 0) - mine);
      if (row.count - mine > 0) out.push({ ...row, count: row.count - mine });
    }
    return out;
  }

  private async respond(req: Arrived, respond: Respond): Promise<void> {
    const before = { ...this.handled };
    const isStream = req.path === "/sdk/v1/stream";
    if (respond.network_error) {
      req.res.socket?.destroy();
    } else if (respond.stream) {
      req.res.writeHead(respond.status ?? 200, respond.headers);
      req.res.flushHeaders();
      this.stream = req.res;
      await until(() => this.handled.opened > before.opened, "the stream to open");
      await this.quiet();
      return;
    } else {
      req.res.writeHead(respond.status ?? 200, respond.headers);
      req.res.end(respond.body !== undefined ? read(respond.body) : undefined);
    }
    if (isStream) await until(() => this.handled.ended > before.ended, "the stream failure to be handled");
    else if (req.path === "/sdk/v1/events") await until(() => this.handled.events > before.events, "the ingest response to be handled");
    else await until(() => this.handled.fetch > before.fetch, "the response to be handled");
    await this.quiet();
  }

  private async send(text: string): Promise<void> {
    if (!this.stream) throw new Error("stream_send without an open stream");
    this.sentBytes += Buffer.byteLength(text);
    this.stream.write(text);
    const sent = this.sentBytes;
    await until(() => this.handled.bytes >= sent, "the stream bytes to be read");
    await this.quiet();
  }

  private async closeStream(): Promise<void> {
    if (!this.stream) throw new Error("stream_close without an open stream");
    const before = this.handled.ended;
    this.stream.end();
    this.stream = undefined;
    await until(() => this.handled.ended > before, "the stream end to be handled");
    await this.quiet();
  }

  /** Fires every timer up to t; with path set, no request to it (or any, for "*") may arrive. */
  private async advance(t: number, path: string | undefined, where: string): Promise<void> {
    for (;;) {
      await this.quiet();
      if (path !== undefined) {
        const unexpected = this.queue.find((r) => path === "*" || r.path === path);
        expect(unexpected, `${where}: unexpected ${unexpected?.path} request at ${this.clock.now()}`).toBeUndefined();
      }
      const next = this.clock.next();
      if (next === undefined || next > t) break;
      this.clock.fireNext();
    }
    this.clock.advanceTo(t);
  }

  private async call(call: NonNullable<Step["call"]>): Promise<unknown> {
    switch (call.op) {
      case "set_override":
        try {
          this.client.overrides.set(need(call.flag), need(call.value));
          return { refused: false };
        } catch (e) {
          if (e instanceof LocalOverridesNotAllowed || e instanceof LocalOverrideRefused) return { refused: true };
          throw e;
        }
      case "evaluate": {
        // One evaluation per call, so `times` is exactly what the summary counts.
        let answer: Answer | undefined;
        for (let n = 0; n < (call.times ?? 1); n++) {
          answer = this.detailAnswer({ flag: need(call.flag), type: need(call.type), default: need(call.default) });
        }
        return answer;
      }
      case "exposure":
        this.client.exposure(this.context, need(call.flag));
        return undefined;
      case "alias":
        for (let n = 0; n < (call.times ?? 1); n++) {
          this.client.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
        }
        return undefined;
      case "flush":
        // Not awaited: the batch it starts is answered by a later step.
        void this.client.flush();
        await settle();
        return undefined;
      default:
        throw new Error(`the scenario asks for the call ${call.op}, which this SDK does not have`);
    }
  }

  /** What `getDetail` serves for flag with default, as an answer: one evaluation. */
  private detailAnswer(a: Pick<Answer, "flag" | "type" | "default">): Answer {
    const d = this.client.getDetail(this.context, a.flag, a.default);
    return {
      flag: a.flag,
      type: a.type,
      default: a.default,
      value: d.value,
      ...(d.variation !== undefined && { variation: d.variation }),
      reason: d.reason,
      ...(d.experiment && { experiment: experimentFields(d.experiment) }),
    };
  }

  /**
   * What the getter of type and `getDetail` serve for flag with default.
   * This is the runner looking, not the scenario calling, so both
   * evaluations are remembered and discounted from the summary a later step
   * asserts on.
   */
  answer(a: Pick<Answer, "flag" | "type" | "default">): Answer {
    const c = this.client;
    const value =
      a.type === "boolean"
        ? c.getBool(this.context, a.flag, a.default as boolean)
        : a.type === "string"
          ? c.getString(this.context, a.flag, a.default as string)
          : a.type === "number"
            ? c.getNumber(this.context, a.flag, a.default as number)
            : c.getJson(this.context, a.flag, a.default);
    const d = c.getDetail(this.context, a.flag, a.default);
    const id = JSON.stringify([a.flag, d.variation ?? null, d.reason]);
    this.observed.set(id, (this.observed.get(id) ?? 0) + 2);
    return {
      flag: a.flag,
      type: a.type,
      default: a.default,
      value,
      ...(d.variation !== undefined && { variation: d.variation }),
      reason: d.reason,
      ...(d.experiment && { experiment: experimentFields(d.experiment) }),
    };
  }

  private check(e: NonNullable<Step["expect"]>, result: unknown, where: string): void {
    if (e.stage !== undefined) expect(this.client.stage, `${where}: stage`).toBe(e.stage);
    if (e.stale_since !== undefined) {
      expect(this.client.status().staleSince !== undefined, `${where}: stale_since`).toBe(e.stale_since === "set");
    }
    const answers = e.answers ?? (e.answers_of !== undefined ? scenarios.answers[e.answers_of] : undefined);
    for (const a of answers ?? []) expect(this.answer(a), `${where}: answer for ${a.flag}`).toStrictEqual(a);
    for (const [event, n] of Object.entries(e.logs ?? {})) {
      expect(this.logs.filter((l) => l === event), `${where}: ${event} logs`).toHaveLength(n);
    }
    if (e.result !== undefined) expect(result, `${where}: result`).toStrictEqual(e.result);
    if (e.refused !== undefined) expect(result, `${where}: refused`).toStrictEqual({ refused: e.refused });
    if (e.recorded !== undefined) {
      expect(this.recorder?.countsByType(), `${where}: the recorder's events`).toStrictEqual(e.recorded);
    }
  }
}

const runners: Runner[] = [];
afterEach(async () => {
  for (const r of runners.splice(0)) await r.stop();
});

async function runner(context: Context, given: Scenario["given"]): Promise<Runner> {
  const r = new Runner(context);
  runners.push(r);
  await r.start(given);
  return r;
}

// ---------------------------------------------------------------- tests

const serverScenarios = scenarios.scenarios.filter((s) => s.mode === "server");

describe("protocol scenarios, server mode", () => {
  test("there are server-mode scenarios, and every one of them runs whole", () => {
    expect(serverScenarios.length).toBeGreaterThan(0);
  });

  test.each(serverScenarios.map((s) => [s.name, s] as const))("%s", async (_, s) => {
    const r = await runner(scenarios.context, s.given);
    await r.run(s.steps);
  });
});

describe("protocol degradation cases, the ruleset endpoint", () => {
  const rulesetCases = cases.cases.filter((c) => c.endpoint === "GET /sdk/v1/ruleset");

  test("there are ruleset cases", () => {
    expect(rulesetCases.length).toBeGreaterThan(0);
  });

  test.each(rulesetCases.map((c) => [c.name, c] as const))("%s", async (_, c) => {
    const options = { sse: false, poll_interval_ms: 30_000 };
    const r = await runner(cases.context, { options });
    const held = JSON.parse(read(c.held)) as { version: number };
    const ok = { "Content-Type": "application/json", ETag: `"${held.version}"` };
    const accept = c.outcome === "accept";
    await r.run([
      { request: { method: "GET", path: "/sdk/v1/ruleset" }, respond: { status: 200, headers: ok, body: c.held }, expect: { stage: "fresh" } },
      {
        request: { method: "GET", path: "/sdk/v1/ruleset", headers: { "If-None-Match": `"${held.version}"` }, delay_ms: { min: 30_000, max: 30_000 } },
        respond: c.response,
        expect: { stage: accept ? "fresh" : "last_good", stale_since: accept ? "unset" : "set", answers: c.answers, ...(c.logs && { logs: c.logs }) },
      },
    ]);
  });
});

describe("protocol degradation cases, the events endpoint", () => {
  const eventsCases = cases.cases.filter((c) => c.endpoint === "POST /sdk/v1/events");

  test("there are events cases", () => {
    expect(eventsCases.length).toBeGreaterThan(0);
  });

  // The batch was answered, so whatever it rejected, and whatever the
  // rejection is called, is dropped: nothing is sent again.
  test.each(eventsCases.map((c) => [c.name, c] as const))("%s", async (_, c) => {
    const r = await runner(cases.context, {
      options: { sse: false, poll_interval_ms: 3_600_000, flush_interval_ms: 3_600_000, send_summaries: false },
    });
    const ok = { "Content-Type": "application/json", ETag: '"1"' };
    await r.run([
      { request: { method: "GET", path: "/sdk/v1/ruleset" }, respond: { status: 200, headers: ok, body: "server/ruleset.json" }, expect: { stage: "fresh" } },
      { call: { op: "alias", times: 12 } },
      { call: { op: "flush" } },
      { request: { method: "POST", path: "/sdk/v1/events" }, respond: c.response },
      { call: { op: "flush" } },
      { no_request: { path: "/sdk/v1/events", for_ms: 600_000 } },
    ]);
  });
});
