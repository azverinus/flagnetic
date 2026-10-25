// Runs the protocol fixtures of conformance/protocol against browser client
// mode: every client-mode scenario of scenarios.json,
// versioning-scenarios.json, experiment-scenarios.json and
// page-scenarios.json, whole, and the
// evaluate degradation cases of degradation/cases.json. It runs in Node and
// in a real browser, so the fixtures are bundled through import.meta.glob and
// the server is mocked at `fetch` rather than on a socket. Time is virtual:
// the client schedules on a clock this runner advances, so a window of 15
// minutes is checked exactly and costs nothing.
import { afterEach, describe, expect, test } from "vitest";
import { createClientWith, type ClientTraceEvent, type FlagneticClient } from "../src/client/client.js";
import { MemoryStore, StoreKeys } from "../src/client/storage.js";
import type { ClientOptions, Context, Experiment, JsonValue, LogEvent } from "../src/client/types.js";
import type { PageHandlers } from "../src/client/page.js";
import { LocalOverrideRefused, LocalOverridesNotAllowed } from "../src/client.js";
import { MockNetwork, type Pending, type Reply } from "./support/mock-network.js";
import { VirtualClock } from "./support/virtual-clock.js";

const key = "fn_cli_0123456789abcdefghijklmnopqrstuvwxyzABCD";
const baseUrl = "http://flagnetic.test";

// ---------------------------------------------------------------- fixtures

const prefix = "../../../conformance/protocol/";
const files = import.meta.glob<string>("../../../conformance/protocol/**/*.{json,txt}", {
  eager: true,
  query: "?raw",
  import: "default",
});
const fixtures = new Map(Object.entries(files).map(([path, text]) => [path.slice(prefix.length), text]));

function read(path: string): string {
  const text = fixtures.get(path);
  if (text === undefined) throw new Error(`no protocol fixture ${path}`);
  return text;
}

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
    /** Query parameters the request's URL must carry with these values. */
    readonly query?: Record<string, string>;
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
  readonly call?: {
    readonly op: string;
    readonly flag?: string;
    readonly type?: Answer["type"];
    readonly default?: JsonValue;
    readonly value?: JsonValue;
    readonly times?: number;
  };
  readonly expect?: {
    readonly stage?: string;
    readonly stale_since?: "set" | "unset";
    readonly answers_of?: string;
    readonly answers?: readonly Answer[];
    readonly logs?: Record<string, number>;
    readonly result?: Answer;
    readonly refused?: boolean;
    readonly recorded?: Record<string, number>;
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
// What only the SDKs decide (versioning and compatibility), in the same
// step language over the same server-produced documents.
scenarios.scenarios.push(...(JSON.parse(read("versioning-scenarios.json")) as { scenarios: Scenario[] }).scenarios);
// Running experiments: their own answers join those of the scenarios above.
const experiments = JSON.parse(read("experiment-scenarios.json")) as { answers: Record<string, Answer[]>; scenarios: Scenario[] };
Object.assign(scenarios.answers, experiments.answers);
scenarios.scenarios.push(...experiments.scenarios);
// The page a browser client runs in: visibility, the network coming back
// and the beacon of a hidden or closing page.
scenarios.scenarios.push(...(JSON.parse(read("page-scenarios.json")) as { scenarios: Scenario[] }).scenarios);

const cases = JSON.parse(read("degradation/cases.json")) as {
  context: Context;
  cases: { name: string; endpoint: string; held: string; response: Respond; outcome: string; answers: Answer[] }[];
};

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

// ---------------------------------------------------------------- the runner

function need<T>(v: T | undefined): T {
  if (v === undefined) throw new Error("the scenario step lacks a field its call needs");
  return v;
}

/** Lets the promises the client is waiting on run to completion. */
async function settle(): Promise<void> {
  for (let i = 0; i < 8; i++) await new Promise<void>((resolve) => setTimeout(resolve, 0));
}

class Runner {
  readonly clock = new VirtualClock();
  readonly net = new MockNetwork(this.clock);
  readonly logs: LogEvent[] = [];
  readonly store = new MemoryStore();
  client!: FlagneticClient;
  private handled = { fetch: 0, events: 0 };
  /** The event IDs of the ingest batch each step answered. */
  private readonly batches = new Map<number, string[]>();
  /** Evaluations this runner made to look at an answer, by (flag, variation, reason). */
  private readonly observed = new Map<string, number>();
  private step = 0;
  /** The client's subscription to the page, which the page calls drive. */
  private page: PageHandlers | undefined;
  private visible = true;

  constructor(private readonly context: Context) {}

  async start(given: Scenario["given"]): Promise<void> {
    const o = given.options;
    if (given.stored !== undefined) {
      this.store.setItem(
        new StoreKeys(key).lastGood(this.context.units ?? {}),
        JSON.stringify({
          units: this.context.units ?? {},
          received_at: new Date(0).toISOString(),
          values: JSON.parse(read(given.stored)) as unknown,
        }),
      );
    }
    const options: ClientOptions = {
      context: this.context,
      store: this.store,
      stream: o.sse === true,
      pollInterval: o.poll_interval_ms as number,
      flushInterval: o.flush_interval_ms as number,
      sendSummaries: o.send_summaries === true,
      sendExposures: o.send_exposures !== false,
      allowOverrides: o.allow_overrides === true,
      logger: (_level, event) => this.logs.push(event),
      ...(given.offline !== undefined && { offline: read(given.offline) }),
    };
    this.client = createClientWith(key, baseUrl, options, {
      clock: this.clock,
      now: () => this.clock.now(),
      random: () => 0.999999,
      fetch: this.net.fetch,
      page: {
        visible: () => this.visible,
        listen: (handlers) => {
          this.page = handlers;
          return () => {
            this.page = undefined;
          };
        },
      },
      beacon: this.net.beacon,
      trace: (e: ClientTraceEvent) => {
        if (e === "fetch_handled") this.handled.fetch++;
        else this.handled.events++;
      },
    });
    await settle();
  }

  async stop(): Promise<void> {
    const closing = this.client.close();
    // close waits on its own timeout, which is on the virtual clock.
    for (let i = 0; i < 100 && this.clock.next() !== undefined; i++) {
      this.clock.fireNext();
      await settle();
    }
    await closing;
  }

  async run(steps: readonly Step[]): Promise<void> {
    let stepStart = this.clock.now();
    for (const [i, step] of steps.entries()) {
      this.step = i;
      const where = `step ${i}${step.note ? ` (${step.note})` : ""}`;
      if (step.request) await this.request(step.request, step.respond ?? { status: 200 }, stepStart, where);
      if (step.stream_send !== undefined) await this.streamSend(step.stream_send);
      if (step.stream_close === true) await this.streamClose();
      if (step.advance_ms !== undefined) await this.advance(this.clock.now() + step.advance_ms, undefined, where);
      if (step.no_request) await this.advance(this.clock.now() + step.no_request.for_ms, step.no_request.path ?? "*", where);
      const result = step.call ? await this.call(step.call) : undefined;
      if (step.expect) this.check(step.expect, result, where);
      stepStart = this.clock.now();
    }
    expect(this.net.pending.map((p) => `${p.method} ${p.path}`), "requests left unanswered after the last step").toStrictEqual([]);
  }

  private async request(want: NonNullable<Step["request"]>, respond: Respond, stepStart: number, where: string): Promise<void> {
    const window = want.delay_ms ?? { min: 0, max: 0 };
    let req: Pending | undefined;
    for (;;) {
      await settle();
      req = this.net.waiting(want.path)[0];
      if (req) break;
      const next = this.clock.next();
      if (next === undefined || next - stepStart > window.max) {
        throw new Error(
          `${where}: no ${want.path} request within ${window.max} ms (next timer at +${next === undefined ? "never" : next - stepStart})`,
        );
      }
      this.clock.fireNext();
    }
    const elapsed = req.at - stepStart;
    expect(elapsed, `${where}: delay of ${want.path}`).toBeGreaterThanOrEqual(window.min);
    expect(elapsed, `${where}: delay of ${want.path}`).toBeLessThanOrEqual(window.max);
    expect(req.method, where).toBe(want.method);
    for (const [name, value] of Object.entries(want.query ?? {})) {
      expect(req.url.searchParams.get(name), `${where}: query ${name}`).toBe(value.replace("<sdk key>", key));
    }
    for (const [name, value] of Object.entries(want.headers ?? {})) {
      expect(req.headers[name.toLowerCase()], `${where}: header ${name}`).toBe(value.replace("<sdk key>", key));
    }
    for (const name of want.absent_headers ?? []) {
      expect(req.headers[name.toLowerCase()], `${where}: header ${name} absent`).toBeUndefined();
    }
    if (req.kind === "stream") {
      // The fixtures leave a client key's stream authentication open; this
      // SDK reads the stream over fetch, so the key goes in a header and
      // never in the URL.
      expect(req.headers.authorization, `${where}: the change stream carries the client key`).toBe(`Bearer ${key}`);
      expect(req.url.search, `${where}: nothing in the stream's URL`).toBe("");
    }
    const events = want.events ? this.checkEvents(want.events, req, where) : undefined;
    if (events) this.batches.set(this.step, events);
    await this.answer(req, respond);
  }

  private checkEvents(want: EventsExpectation, req: Pending, where: string): string[] {
    const body = JSON.parse(need(req.body)) as {
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
      const counts = this.discountObserved(summary?.counts as { flag: string; variation: string | null; reason: string; count: number }[]);
      const order = (rows: typeof counts): typeof counts => [...rows].sort((a, b) => `${a.flag}${a.variation ?? ""}${a.reason}`.localeCompare(`${b.flag}${b.variation ?? ""}${b.reason}`));
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
      expect(need(req.body).includes(absent), `${where}: ${JSON.stringify(absent)} is nowhere in the body`).toBe(false);
    }
    return ids;
  }

  // Subtracts the evaluations this runner made while checking answers.
  private discountObserved(
    counts: { flag: string; variation: string | null; reason: string; count: number }[],
  ): { flag: string; variation: string | null; reason: string; count: number }[] {
    const out: typeof counts = [];
    for (const row of counts) {
      const id = JSON.stringify([row.flag, row.variation, row.reason]);
      const mine = Math.min(this.observed.get(id) ?? 0, row.count);
      this.observed.set(id, (this.observed.get(id) ?? 0) - mine);
      if (row.count - mine > 0) out.push({ ...row, count: row.count - mine });
    }
    return out;
  }

  private async answer(req: Pending, respond: Respond): Promise<void> {
    const before = { ...this.handled };
    const reply: Reply = {
      ...(respond.status !== undefined && { status: respond.status }),
      ...(respond.headers && { headers: respond.headers }),
      ...(respond.body !== undefined && { body: this.responseBody(req, respond.body) }),
      ...(respond.network_error === true && { networkError: true }),
      ...(respond.stream === true && { stream: true }),
    };
    req.answer(reply);
    await settle();
    if (req.kind === "fetch" && req.path === "/sdk/v1/events") {
      await until(() => this.handled.events > before.events, "the ingest response to be handled");
    } else if (req.kind === "fetch") {
      await until(() => this.handled.fetch > before.fetch, "the response to be handled");
    }
    await settle();
  }

  // A 202 names the event IDs the document was produced with; the mock puts
  // the IDs of this request's own events in their place.
  private responseBody(req: Pending, path: string): string {
    const text = read(path);
    if (req.path !== "/sdk/v1/events" || req.body === undefined) return text;
    const doc = JSON.parse(text) as { rejected?: { index: number; event_id: string }[] };
    if (!doc.rejected || doc.rejected.length === 0) return text;
    const sent = (JSON.parse(req.body) as { events: { event_id: string }[] }).events;
    doc.rejected = doc.rejected.map((r) => ({ ...r, event_id: sent[r.index]?.event_id ?? r.event_id }));
    return JSON.stringify(doc);
  }

  private async streamSend(text: string): Promise<void> {
    this.net.send(text);
    await settle();
  }

  private async streamClose(): Promise<void> {
    this.net.closeStreams();
    await settle();
  }

  /** Fires every timer up to t; with path set, no request to it (or any, for "*") may arrive. */
  private async advance(t: number, path: string | undefined, where: string): Promise<void> {
    for (;;) {
      await settle();
      if (path !== undefined) {
        const unexpected = this.net.waiting(path === "*" ? undefined : path)[0];
        expect(unexpected && `${unexpected.method} ${unexpected.path}`, `${where}: unexpected request at ${this.clock.now()}`).toBeFalsy();
      }
      const next = this.clock.next();
      if (next === undefined || next > t) break;
      this.clock.fireNext();
    }
    this.clock.advanceTo(t);
    await settle();
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
        this.client.exposure(need(call.flag));
        return undefined;
      case "alias":
        for (let n = 0; n < (call.times ?? 1); n++) {
          this.client.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
        }
        return undefined;
      case "visibility_hidden":
        this.visible = false;
        this.page?.hidden();
        await settle();
        return undefined;
      case "pagehide":
        // A page being unloaded or put in the back-forward cache: the
        // client hears it as hidden whatever the visibility said before.
        this.visible = false;
        this.page?.hidden();
        await settle();
        return undefined;
      case "visibility_visible":
        this.visible = true;
        this.page?.visible();
        await settle();
        return undefined;
      case "online":
        this.page?.online();
        await settle();
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
    const d = this.client.getDetail(a.flag, a.default);
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
   * What the getter of type and `getDetail` serve for flag with default, as
   * an answer. This is the runner looking, not the scenario calling, so both
   * evaluations are remembered and discounted from the summary a later step
   * asserts on: the scenario's `answers` are observations, its `call` steps
   * are what the counts are about.
   */
  private observe(a: Pick<Answer, "flag" | "type" | "default">): Answer {
    const c = this.client;
    const value =
      a.type === "boolean"
        ? c.getBool(a.flag, a.default as boolean)
        : a.type === "string"
          ? c.getString(a.flag, a.default as string)
          : a.type === "number"
            ? c.getNumber(a.flag, a.default as number)
            : c.getJson(a.flag, a.default);
    const d = c.getDetail(a.flag, a.default);
    expect(value, `the ${a.type} getter and getDetail agree on ${a.flag}`).toStrictEqual(d.value);
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
    for (const a of answers ?? []) expect(this.observe(a), `${where}: answer for ${a.flag}`).toStrictEqual(a);
    for (const [event, n] of Object.entries(e.logs ?? {})) {
      expect(this.logs.filter((l) => l === event), `${where}: ${event} logs`).toHaveLength(n);
    }
    if (e.result !== undefined) expect(result, `${where}: result`).toStrictEqual(e.result);
    if (e.refused !== undefined) expect(result, `${where}: refused`).toStrictEqual({ refused: e.refused });
    if (e.recorded !== undefined) {
      const counts: Record<string, number> = {};
      for (const ev of this.client.offline?.recorded() ?? []) counts[ev.type] = (counts[ev.type] ?? 0) + 1;
      expect(counts, `${where}: the recorder's events`).toStrictEqual(e.recorded);
    }
  }
}

async function until(predicate: () => boolean, what: string, timeout = 5000): Promise<void> {
  const deadline = Date.now() + timeout;
  while (!predicate()) {
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await new Promise<void>((resolve) => setTimeout(resolve, 1));
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

const clientScenarios = scenarios.scenarios.filter((s) => s.mode === "client");

describe("protocol scenarios, browser client mode", () => {
  test("there are client-mode scenarios", () => {
    expect(clientScenarios.length).toBeGreaterThan(0);
  });

  test.each(clientScenarios.map((s) => [s.name, s] as const))("%s", async (_, s) => {
    const r = await runner(scenarios.context, s.given);
    await r.run(s.steps);
  });
});

describe("protocol degradation cases, the evaluate endpoint", () => {
  const evaluateCases = cases.cases.filter((c) => c.endpoint === "POST /sdk/v1/evaluate");

  test("there are evaluate cases", () => {
    expect(evaluateCases.length).toBeGreaterThan(0);
  });

  test.each(evaluateCases.map((c) => [c.name, c] as const))("%s", async (_, c) => {
    const r = await runner(cases.context, { options: { sse: false, poll_interval_ms: 30_000, flush_interval_ms: 3_600_000 } });
    const ok = { "Cache-Control": "no-store", "Content-Type": "application/json" };
    const accept = c.outcome === "accept";
    await r.run([
      { request: { method: "POST", path: "/sdk/v1/evaluate" }, respond: { status: 200, headers: ok, body: c.held }, expect: { stage: "fresh" } },
      {
        request: { method: "POST", path: "/sdk/v1/evaluate", delay_ms: { min: 30_000, max: 30_000 } },
        respond: c.response,
        expect: { stage: accept ? "fresh" : "last_good", stale_since: accept ? "unset" : "set", answers: c.answers },
      },
    ]);
  });
});
