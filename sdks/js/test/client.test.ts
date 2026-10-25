// The browser client's behaviour the protocol fixtures leave to each SDK:
// construction, browser storage (a store that throws included), bootstrap,
// the refetch triggers a tab has, the change stream, the persisted event
// queue, local overrides and offline mode. It runs in Node and in a real
// browser, on a virtual clock, against the mock of `fetch` in
// test/support/mock-network.ts.
import { afterEach, describe, expect, test } from "vitest";
import { createClient, createClientWith, type ClientInternals, type ClientTraceEvent, type FlagneticClient, type Timing } from "../src/client/client.js";
import { LocalOverridesNotAllowed } from "../src/client.js";
import { testServer } from "../src/server.js";
import { ruleset } from "./support/flags-fixture.js";
import { MemoryStore, StoreKeys } from "../src/client/storage.js";
import type { Page, PageHandlers } from "../src/client/page.js";
import type { ClientOptions, ClientStore, Context, LogEvent } from "../src/client/types.js";
import { MockNetwork, type Pending } from "./support/mock-network.js";
import { VirtualClock } from "./support/virtual-clock.js";

const key = "fn_cli_0123456789abcdefghijklmnopqrstuvwxyzABCD";
const serverKey = "fn_srv_0123456789abcdefghijklmnopqrstuvwxyzABCD";
const baseUrl = "http://flagnetic.test";
const context: Context = { units: { anonymous: "d-8f1c" }, attributes: { country: "DE" } };
/** The keys this connection writes; another SDK key writes its own. */
const keys = new StoreKeys(key);

/** A values document as remote evaluation serves it. */
function values(version: number, flags: Record<string, unknown>, extra: Record<string, unknown> = {}): Record<string, unknown> {
  return { schema_version: 1, ruleset_version: version, environment: { key: "development", protected: false }, flags, ...extra };
}

const boolFlag = (value: boolean, reason = "targeting_match"): Record<string, unknown> => ({
  type: "boolean",
  value,
  variation: String(value),
  reason,
  override_influenced: false,
  automatic_exposure: false,
});

const experimentFlag = (value: boolean, automatic: boolean, experiment = "checkout_v2_exp1"): Record<string, unknown> => ({
  type: "boolean",
  value,
  variation: "treatment",
  reason: "experiment",
  override_influenced: false,
  automatic_exposure: automatic,
  experiment: {
    key: experiment,
    variation: "treatment",
    unit_type: "anonymous",
    unit_id: "d-8f1c",
    inclusion_salt: "a1b2c3d4e5f60718",
    variation_salt: "c3d4e5f607182930",
    inclusion_bucket: 3537,
    variation_bucket: 2898,
    override: false,
  },
});

/** A tab a test drives: visible or hidden, with the network coming and going. */
class FakePage implements Page {
  private shown = true;
  private readonly handlers: PageHandlers[] = [];

  visible(): boolean {
    return this.shown;
  }

  listen(handlers: PageHandlers): () => void {
    this.handlers.push(handlers);
    return () => {
      const at = this.handlers.indexOf(handlers);
      if (at >= 0) this.handlers.splice(at, 1);
    };
  }

  show(): void {
    this.shown = true;
    for (const h of [...this.handlers]) h.visible();
  }

  hide(): void {
    this.shown = false;
    for (const h of [...this.handlers]) h.hidden();
  }

  online(): void {
    for (const h of [...this.handlers]) h.online();
  }
}

/** The `from_unit_id` of every alias a request carries. */
function unitIds(req: Pending): string[] {
  return (JSON.parse(req.body ?? "{}") as { events: { from_unit_id: string }[] }).events.map((e) => e.from_unit_id);
}

async function settle(): Promise<void> {
  for (let i = 0; i < 8; i++) await new Promise<void>((resolve) => setTimeout(resolve, 0));
}

class Harness {
  readonly clock = new VirtualClock();
  readonly net = new MockNetwork(this.clock);
  readonly page = new FakePage();
  readonly logs: { event: LogEvent; fields: Readonly<Record<string, unknown>> }[] = [];
  readonly clients: FlagneticClient[] = [];
  handled = { fetch: 0, events: 0 };

  /** The page's beacon: the mock network's, which takes every one, unless a test replaces it. */
  beacon: ((url: string, body: string) => boolean) | null = this.net.beacon;

  client(options: ClientOptions = {}, sdkKey = key, timing: Partial<Timing> = {}, internals: ClientInternals = {}): FlagneticClient {
    const c = createClientWith(
      sdkKey,
      baseUrl,
      { context, logger: (_l, event, fields) => this.logs.push({ event, fields }), ...options },
      {
        clock: this.clock,
        now: () => this.clock.now(),
        random: () => 0.5,
        fetch: this.net.fetch,
        page: this.page,
        beacon: this.beacon,
        timing,
        trace: (e: ClientTraceEvent) => {
          if (e === "fetch_handled") this.handled.fetch++;
          else this.handled.events++;
        },
        ...internals,
      },
    );
    this.clients.push(c);
    return c;
  }

  /** Fires timers until a request arrives at path, or until none is left. */
  async waitFor(path: string, within = 600_000): Promise<Pending> {
    const deadline = this.clock.now() + within;
    for (;;) {
      await settle();
      const req = this.net.waiting(path)[0];
      if (req) return req;
      const next = this.clock.next();
      if (next === undefined || next > deadline) throw new Error(`no ${path} request within ${within} ms`);
      this.clock.fireNext();
    }
  }

  /** Answers one request at path with a values document and waits for it to be handled. */
  async serve(path: string, body: unknown, status = 200): Promise<void> {
    const req = await this.waitFor(path);
    const before = { ...this.handled };
    req.answer({ status, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    await settle();
    for (let i = 0; i < 50 && this.handled.fetch === before.fetch && this.handled.events === before.events; i++) await settle();
  }

  /** Advances the virtual clock by ms, firing every timer on the way. */
  async advance(ms: number): Promise<void> {
    const until = this.clock.now() + ms;
    for (;;) {
      await settle();
      const next = this.clock.next();
      if (next === undefined || next > until) break;
      this.clock.fireNext();
    }
    this.clock.advanceTo(until);
    await settle();
  }

  /** Moves the clock a millisecond, so that the next event gets an ID of its own. */
  tick(): void {
    this.clock.advanceTo(this.clock.now() + 1);
  }

  count(event: LogEvent): number {
    return this.logs.filter((l) => l.event === event).length;
  }

  async stop(): Promise<void> {
    for (const c of this.clients) {
      const closing = c.close();
      for (let i = 0; i < 100 && this.clock.next() !== undefined; i++) {
        this.clock.fireNext();
        await settle();
      }
      await closing;
    }
  }
}

const harnesses: Harness[] = [];
function harness(): Harness {
  const h = new Harness();
  harnesses.push(h);
  return h;
}
afterEach(async () => {
  for (const h of harnesses.splice(0)) await h.stop();
});

// ---------------------------------------------------------------- construction

describe("construction", () => {
  test("a server key is the one mistake the client refuses loudly", () => {
    expect(() => createClient(serverKey, baseUrl)).toThrow(/client key/);
  });

  test("a client key builds a client that starts at built_in and serves its defaults", async () => {
    const h = harness();
    const c = h.client({ store: false });
    expect(c.stage).toBe("built_in");
    expect(c.getBool("checkout", true)).toBe(true);
    expect(c.getDetail("checkout", true)).toMatchObject({ reason: "fallback", errorCode: "no_values", stage: "built_in" });
    await h.waitFor("/sdk/v1/evaluate");
  });

  test("a client deferring its start makes no request until its first context, then starts as any client does: the stream first, then one evaluation of that context", async () => {
    const h = harness();
    const c = h.client({ store: false, stream: true, pollInterval: 3_600_000 }, key, {}, { deferStart: true });
    h.page.hide();
    h.page.show();
    h.page.online();
    await h.advance(600_000);
    expect(h.net.seen, "nothing is evaluated for the constructor's context").toHaveLength(0);

    const switched = c.setContext({ units: { user: "u-2002" } });
    const stream = await h.waitFor("/sdk/v1/stream", 0);
    expect(h.net.waiting("/sdk/v1/evaluate"), "the evaluation waits for the stream, so no change falls between the two").toHaveLength(0);
    stream.answer({ stream: true, body: "retry: 3000\n\n" });
    const evaluate = await h.waitFor("/sdk/v1/evaluate", 0);
    expect(JSON.parse(evaluate.body ?? "{}")).toMatchObject({ context: { units: { user: "u-2002" } } });
    evaluate.answer({ status: 200, headers: { "Content-Type": "application/json" }, body: JSON.stringify(values(1, { checkout: boolFlag(true) })) });
    expect(await switched).toBe("fresh");
    await h.advance(10_000);
    expect(h.net.seen.filter((r) => r.path === "/sdk/v1/evaluate"), "one evaluation for the page load").toHaveLength(1);
  });

  test("a client deferring its start with a stream that does not open evaluates its first context after the grace period", async () => {
    const h = harness();
    const c = h.client({ store: false, stream: true, pollInterval: 3_600_000 }, key, {}, { deferStart: true });
    const switched = c.setContext({ units: { user: "u-2002" } });
    const evaluate = await h.waitFor("/sdk/v1/evaluate", 2_000);
    evaluate.answer({ status: 200, headers: { "Content-Type": "application/json" }, body: JSON.stringify(values(1, { checkout: boolFlag(true) })) });
    expect(await switched).toBe("fresh");
  });

  test("last good loads synchronously from browser storage under the unit-set key", async () => {
    const h = harness();
    const store = new MemoryStore();
    store.setItem(
      keys.lastGood(context.units ?? {}),
      JSON.stringify({ units: context.units, received_at: "2026-01-01T00:00:00.000Z", values: values(7, { checkout: boolFlag(true) }) }),
    );
    // No await: the values must serve from the moment the constructor returns.
    const c = h.client({ store });
    expect(c.stage).toBe("last_good");
    expect(c.getBool("checkout", false)).toBe(true);
    expect(c.status().rulesetVersion).toBe(7);
    expect(c.getDetail("checkout", false).receivedAt?.toISOString()).toBe("2026-01-01T00:00:00.000Z");
    await h.waitFor("/sdk/v1/evaluate");
  });

  test("an entry stored for another unit set is not served", async () => {
    const h = harness();
    const store = new MemoryStore();
    store.setItem(
      keys.lastGood(context.units ?? {}),
      JSON.stringify({ units: { anonymous: "someone-else" }, received_at: "2026-01-01T00:00:00.000Z", values: values(7, { checkout: boolFlag(true) }) }),
    );
    const c = h.client({ store });
    expect(c.stage).toBe("built_in");
    expect(h.count("store_failed")).toBe(1);
    await h.waitFor("/sdk/v1/evaluate");
  });

  test("values stored under another SDK key are not served, whatever version they carry", async () => {
    const h = harness();
    const store = new MemoryStore();
    const other = new StoreKeys("fn_cli_9876543210zyxwvutsrqponmlkjihgfedcbaZYXW");
    store.setItem(
      other.lastGood(context.units ?? {}),
      JSON.stringify({ units: context.units, received_at: "2026-01-01T00:00:00.000Z", values: values(9, { checkout: boolFlag(true) }) }),
    );
    // Another environment's ruleset is another environment's: neither its
    // values nor its version take part in this connection.
    const c = h.client({ store });
    expect(c.stage).toBe("built_in");
    await h.serve("/sdk/v1/evaluate", values(2, { checkout: boolFlag(false) }));
    expect(c.stage).toBe("fresh");
    expect(c.getBool("checkout", true)).toBe(false);
    expect(c.status().rulesetVersion).toBe(2);
    expect(store.getItem(other.lastGood(context.units ?? {})), "the other connection's entry is left alone").not.toBeNull();
  });

  test("two connections on one store keep their own values and their own queue", async () => {
    const h = harness();
    const store = new MemoryStore();
    const otherKey = "fn_cli_9876543210zyxwvutsrqponmlkjihgfedcbaZYXW";
    const other = new StoreKeys(otherKey);

    const production = h.client({ store, pollInterval: 3_600_000, flushInterval: 3_600_000, sendSummaries: false });
    await h.serve("/sdk/v1/evaluate", values(4, { checkout: boolFlag(true) }));
    production.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    void production.flush();
    (await h.waitFor("/sdk/v1/events")).answer({ status: 500 });
    await settle();

    const staging = h.client({ store, pollInterval: 3_600_000, flushInterval: 3_600_000, sendSummaries: false }, otherKey);
    expect(staging.stage, "the other key's values are not this key's").toBe("built_in");
    await h.serve("/sdk/v1/evaluate", values(1, { checkout: boolFlag(false) }));
    expect(staging.getBool("checkout", true)).toBe(false);
    expect(production.getBool("checkout", false), "each client keeps serving its own environment").toBe(true);

    expect(store.getItem(keys.lastGood(context.units ?? {}))).not.toBeNull();
    expect(store.getItem(other.lastGood(context.units ?? {}))).not.toBeNull();
    expect(store.getItem(keys.events), "the queue stays with the key that recorded it").not.toBeNull();
    expect(store.getItem(other.events), "and is never picked up under another key").toBeNull();
  });

  test("a storage that throws yields an in-memory fallback, never a crash", async () => {
    const h = harness();
    const throwing: ClientStore = {
      getItem() {
        throw new DOMException("The operation is insecure.", "SecurityError");
      },
      setItem() {
        throw new DOMException("The quota has been exceeded.", "QuotaExceededError");
      },
      removeItem() {
        throw new Error("no");
      },
    };
    const c = h.client({ store: throwing, sendSummaries: false });
    expect(c.stage).toBe("built_in");
    expect(c.getBool("checkout", false)).toBe(false);
    await h.serve("/sdk/v1/evaluate", values(1, { checkout: boolFlag(true) }));
    // The fetch succeeded and persisting it failed: values still serve.
    expect(c.stage).toBe("fresh");
    expect(c.getBool("checkout", false)).toBe(true);
    expect(h.count("store_failed")).toBeGreaterThan(0);
    // The queue lives in memory from here on and still flushes.
    c.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    void c.flush();
    const req = await h.waitFor("/sdk/v1/events");
    expect((JSON.parse(req.body ?? "{}") as { events: { type: string }[] }).events.map((e) => e.type)).toStrictEqual(["alias"]);
    req.answer({ status: 202, body: JSON.stringify({ accepted: 1, duplicates: 0, dropped_rows: 0, rejected: [] }) });
  });

  test("bootstrap accepts what the server SDK's values() renders into a page and loads as fresh", async () => {
    const h = harness();
    // The page was rendered for a German visitor: a private segment decided
    // the value, which the document names only as targeting.
    const server = testServer(ruleset());
    const c = h.client({ store: false, bootstrap: server.values(context) ?? {} });
    expect(c.stage).toBe("fresh");
    expect(c.getDetail("checkout", false)).toMatchObject({ value: true, variation: "on", reason: "targeting_match", rulesetVersion: 7 });
    expect(c.getJson("theme", null)).toStrictEqual({ mode: "dark" });
    expect(await c.ready(0)).toBe("fresh");
    // A bootstrapped client still evaluates.
    await h.waitFor("/sdk/v1/evaluate");
  });

  test("bootstrap takes the document as JSON text too", async () => {
    const h = harness();
    const c = h.client({ store: false, bootstrap: JSON.stringify(values(3, { checkout: boolFlag(true) })) });
    expect(c.getBool("checkout", false)).toBe(true);
    await h.waitFor("/sdk/v1/evaluate");
  });
});

// ---------------------------------------------------------------- evaluation

describe("remote evaluation", () => {
  test("the request carries the context, the client key and the schema version, and no User-Agent", async () => {
    const h = harness();
    h.client({ store: false, appVersion: "1.42.0", platform: "web" });
    const req = await h.waitFor("/sdk/v1/evaluate");
    expect(req.method).toBe("POST");
    expect(req.headers.authorization).toBe(`Bearer ${key}`);
    expect(req.headers["content-type"]).toBe("application/json");
    expect(req.headers["flagnetic-schema-version"]).toBe("1");
    // A browser's CORS preflight allows neither of these.
    expect(req.headers["user-agent"]).toBeUndefined();
    expect(req.headers["accept-encoding"]).toBeUndefined();
    expect(JSON.parse(req.body ?? "{}")).toStrictEqual({
      context: {
        units: { anonymous: "d-8f1c" },
        attributes: { platform: "web", app_version: "1.42.0", sdk: "flagnetic-js", sdk_version: "0.0.0", country: "DE" },
      },
    });
  });

  test("an ignored attribute is logged once per name, however often it arrives", async () => {
    const h = harness();
    const c = h.client({ store: false, pollInterval: 30_000 });
    const doc = values(1, { checkout: boolFlag(true) }, {
      ignored_attributes: [
        { name: "country", code: "undeclared" },
        { name: "plan", code: "type_mismatch" },
      ],
    });
    await h.serve("/sdk/v1/evaluate", doc);
    await h.serve("/sdk/v1/evaluate", values(2, { checkout: boolFlag(true) }, { ignored_attributes: [{ name: "country", code: "undeclared" }] }));
    expect(h.count("attribute_ignored")).toBe(2);
    expect(h.logs.filter((l) => l.event === "attribute_ignored").map((l) => l.fields.name)).toStrictEqual(["country", "plan"]);
    expect(c.stage).toBe("fresh");
  });

  test("ready resolves at its timeout with the stage reached and never rejects", async () => {
    const h = harness();
    const c = h.client({ store: false });
    const ready = c.ready(1_000);
    await h.advance(1_000);
    expect(await ready).toBe("built_in");
  });
});

// ------------------------------------------------------------ refetch triggers

describe("refetch triggers", () => {
  test("a context change serves the previous values until the response and then the diff", async () => {
    const h = harness();
    const store = new MemoryStore();
    const c = h.client({ store, pollInterval: 3_600_000 });
    await h.serve("/sdk/v1/evaluate", values(1, { checkout: boolFlag(true), banner: { type: "string", value: "a", variation: "a", reason: "targeting_match" } }));
    const changed: string[][] = [];
    c.onChange((keys) => changed.push(keys));

    const signedIn = { units: { anonymous: "d-8f1c", user: "u-1001" } };
    const done = c.setContext(signedIn);
    // The previous values keep serving: no flash of defaults.
    expect(c.getBool("checkout", false)).toBe(true);
    expect(c.stage).toBe("last_good");

    const req = await h.waitFor("/sdk/v1/evaluate");
    expect((JSON.parse(req.body ?? "{}") as { context: { units: unknown } }).context.units).toStrictEqual(signedIn.units);
    req.answer({
      status: 200,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(values(2, { checkout: boolFlag(false), banner: { type: "string", value: "a", variation: "a", reason: "targeting_match" } })),
    });
    expect(await done).toBe("fresh");
    expect(c.getBool("checkout", true)).toBe(false);
    // Only the key whose value changed.
    expect(changed).toStrictEqual([["checkout"]]);
    // The new unit set has its own last good entry, the old one keeps its.
    expect(store.getItem(keys.lastGood(signedIn.units))).not.toBeNull();
    expect(store.getItem(keys.lastGood(context.units ?? {}))).not.toBeNull();
  });

  test("the new context's first answer is taken whatever its version, a later lower one is not", async () => {
    const h = harness();
    const c = h.client({ store: false, pollInterval: 30_000 });
    await h.serve("/sdk/v1/evaluate", values(5, { checkout: boolFlag(false) }));

    // A replica a little behind answers the new context.
    const done = c.setContext({ units: { user: "u-1001" } });
    await h.serve("/sdk/v1/evaluate", values(4, { checkout: boolFlag(true) }));
    expect(await done).toBe("fresh");
    expect(c.getBool("checkout", false)).toBe(true);

    // For the same context, a version below the held one is refused again.
    await h.serve("/sdk/v1/evaluate", values(3, { checkout: boolFlag(false) }));
    expect(c.stage).toBe("last_good");
    expect(c.getBool("checkout", false)).toBe(true);
  });

  test("triggers coalesce into the request in flight and start exactly one more", async () => {
    const h = harness();
    const c = h.client({ store: false, pollInterval: 3_600_000 });
    const first = await h.waitFor("/sdk/v1/evaluate");
    const a = c.setContext({ units: { user: "u-1" } });
    const b = c.setContext({ units: { user: "u-2" } });
    expect(h.net.waiting("/sdk/v1/evaluate")).toHaveLength(1);
    first.answer({ status: 200, headers: { "Content-Type": "application/json" }, body: JSON.stringify(values(1, {})) });
    await settle();
    const second = await h.waitFor("/sdk/v1/evaluate");
    expect((JSON.parse(second.body ?? "{}") as { context: { units: unknown } }).context.units).toStrictEqual({ user: "u-2" });
    second.answer({ status: 200, headers: { "Content-Type": "application/json" }, body: JSON.stringify(values(2, {})) });
    expect(await a).toBe("fresh");
    expect(await b).toBe("fresh");
    await settle();
    expect(h.net.waiting("/sdk/v1/evaluate")).toHaveLength(0);
  });

  test("an answer for the context replaced while it was in flight does not serve", async () => {
    const h = harness();
    const c = h.client({ store: false, pollInterval: 3_600_000 });
    await h.serve("/sdk/v1/evaluate", values(1, { checkout: boolFlag(true) }));
    const changed: string[][] = [];
    const stages: string[] = [];
    c.onChange((keys) => changed.push(keys));
    c.onStage((s) => stages.push(s));

    // A refetch for the anonymous context is in flight when the user signs in.
    await h.advance(10_000);
    h.page.online();
    const stale = await h.waitFor("/sdk/v1/evaluate");
    const done = c.setContext({ units: { user: "u-1" } });
    stale.answer({ status: 200, headers: { "Content-Type": "application/json" }, body: JSON.stringify(values(2, { checkout: boolFlag(false) })) });
    await settle();
    expect(c.getBool("checkout", false)).toBe(true);
    expect(c.stage).toBe("last_good");
    expect(changed).toStrictEqual([]);
    expect(h.count("fetch_failed")).toBe(0);

    const fresh = await h.waitFor("/sdk/v1/evaluate");
    expect((JSON.parse(fresh.body ?? "{}") as { context: { units: unknown } }).context.units).toStrictEqual({ user: "u-1" });
    fresh.answer({ status: 200, headers: { "Content-Type": "application/json" }, body: JSON.stringify(values(2, { checkout: boolFlag(false) })) });
    expect(await done).toBe("fresh");
    expect(c.getBool("checkout", true)).toBe(false);
    expect(stages).toStrictEqual(["last_good", "fresh"]);
    expect(changed).toStrictEqual([["checkout"]]);
  });

  test("close aborts the evaluation in flight and records no failure", async () => {
    const h = harness();
    const c = h.client({ store: false, pollInterval: 3_600_000 });
    await h.waitFor("/sdk/v1/evaluate");
    await c.close();
    await settle();
    expect(h.net.waiting("/sdk/v1/evaluate")).toHaveLength(0);
    expect(h.count("fetch_failed")).toBe(0);
    expect(h.count("evaluation_failed")).toBe(0);
    expect(c.stage).toBe("built_in");
  });

  test("visibility and the network coming back refetch, no oftener than the minimum gap", async () => {
    const h = harness();
    h.client({ store: false, pollInterval: 3_600_000 });
    await h.serve("/sdk/v1/evaluate", values(1, {}));
    const before = h.net.seen.length;

    h.page.hide();
    h.page.show();
    h.page.online();
    await settle();
    expect(h.net.seen.length, "within the gap: nothing").toBe(before);

    await h.advance(10_000);
    h.page.online();
    await settle();
    expect(h.net.waiting("/sdk/v1/evaluate"), "after the gap: one request").toHaveLength(1);
  });

  test("the interval polls while visible and stops while hidden", async () => {
    const h = harness();
    h.client({ store: false, pollInterval: 300_000 });
    await h.serve("/sdk/v1/evaluate", values(1, {}));
    h.page.hide();
    await h.advance(1_200_000);
    expect(h.net.waiting("/sdk/v1/evaluate"), "a hidden tab does not poll").toHaveLength(0);
    h.page.show();
    // Becoming visible is a refetch trigger of its own; the interval follows it.
    const onShow = await h.waitFor("/sdk/v1/evaluate");
    onShow.answer({ status: 200, headers: { "Content-Type": "application/json" }, body: JSON.stringify(values(2, {})) });
    await settle();
    await h.advance(299_000);
    expect(h.net.waiting("/sdk/v1/evaluate"), "not before the interval is up").toHaveLength(0);
    await h.advance(2_000);
    expect(h.net.waiting("/sdk/v1/evaluate")).toHaveLength(1);
  });
});

// ---------------------------------------------------------------- the stream

describe("the change stream", () => {
  test("the stream is off unless asked for", async () => {
    const h = harness();
    h.client({ store: false });
    await h.waitFor("/sdk/v1/evaluate");
    await h.advance(60_000);
    expect(h.net.seen.filter((r) => r.path === "/sdk/v1/stream")).toHaveLength(0);
  });

  test("the stream carries the client key in a header the CORS preflight allows, never in the URL", async () => {
    const h = harness();
    h.client({ store: false, stream: true });
    const req = await h.waitFor("/sdk/v1/stream");
    expect(req.url.search).toBe("");
    expect(req.headers).toStrictEqual({ authorization: `Bearer ${key}`, accept: "text/event-stream" });
  });

  test("a ruleset event above the held version triggers exactly one evaluation", async () => {
    const h = harness();
    const c = h.client({ store: false, stream: true, pollInterval: 3_600_000 });
    (await h.waitFor("/sdk/v1/stream")).answer({ stream: true, body: "retry: 3000\n\n" });
    await h.serve("/sdk/v1/evaluate", values(1, { checkout: boolFlag(true) }));
    expect(h.count("sse_connected")).toBe(1);

    h.net.send('event: ruleset\nid: 1\ndata: {"version":1}\n\n');
    await settle();
    expect(h.net.waiting("/sdk/v1/evaluate"), "the held version is not news").toHaveLength(0);

    h.net.send('event: ruleset\nid: 2\ndata: {"version":2}\n\n');
    await settle();
    expect(h.net.waiting("/sdk/v1/evaluate")).toHaveLength(1);
    await h.serve("/sdk/v1/evaluate", values(2, { checkout: boolFlag(false) }));
    expect(c.getBool("checkout", true)).toBe(false);
  });

  test("a stream the server closes is reconnected within 2 s with Last-Event-ID, and the client evaluates again", async () => {
    const h = harness();
    h.client({ store: false, stream: true, pollInterval: 3_600_000 });
    (await h.waitFor("/sdk/v1/stream")).answer({ stream: true, body: 'retry: 3000\n\nevent: ruleset\nid: 1\ndata: {"version":1}\n\n' });
    await h.serve("/sdk/v1/evaluate", values(1, {}));

    h.net.closeStreams();
    await settle();
    const closedAt = h.clock.now();
    const again = await h.waitFor("/sdk/v1/stream", 2_000);
    expect(again.at - closedAt, "jitter only, whatever retry the stream asked for").toBeLessThanOrEqual(2_000);
    expect(again.headers["last-event-id"]).toBe("1");
    again.answer({ stream: true, body: "retry: 3000\n\n" });
    await settle();
    // Versions may have been missed while the stream was down.
    expect(h.net.waiting("/sdk/v1/evaluate")).toHaveLength(1);
  });

  test("a refused key closes the stream, logs key_revoked once, and the stream returns when the key works", async () => {
    const h = harness();
    h.client({ store: false, stream: true, pollInterval: 3_600_000 });
    (await h.waitFor("/sdk/v1/stream")).answer({ stream: true, body: "retry: 3000\n\n" });
    await settle();
    expect(h.net.openStreams).toBe(1);

    for (let i = 0; i < 3; i++) {
      const req = await h.waitFor("/sdk/v1/evaluate");
      req.answer({ status: 401, headers: { "Content-Type": "application/problem+json" }, body: JSON.stringify({ code: "sdk_key_revoked" }) });
      await settle();
    }
    expect(h.count("key_revoked"), "logged once until a fetch succeeds").toBe(1);
    expect(h.net.openStreams, "the stream carries the same key").toBe(0);

    await h.serve("/sdk/v1/evaluate", values(1, { checkout: boolFlag(true) }));
    const back = await h.waitFor("/sdk/v1/stream");
    back.answer({ stream: true, body: "retry: 3000\n\n" });
    await settle();
    expect(h.net.openStreams).toBe(1);
  });
});

describe("the change stream, refused", () => {
  test("a stream answering 401 stays closed, logs key_revoked once, and opens again after an evaluation succeeds", async () => {
    const h = harness();
    h.client({ store: false, stream: true, pollInterval: 30_000 });
    (await h.waitFor("/sdk/v1/stream")).answer({ status: 401, headers: { "Content-Type": "application/problem+json" }, body: "{}" });
    await settle();
    expect(h.count("key_revoked")).toBe(1);
    // The first values do not wait for a stream that will not open.
    await h.serve("/sdk/v1/evaluate", values(1, {}));
    const back = await h.waitFor("/sdk/v1/stream", 1);
    back.answer({ stream: true });
    await settle();
    expect(h.net.openStreams).toBe(1);
    expect(h.net.seen.filter((r) => r.path === "/sdk/v1/stream")).toHaveLength(2);
  });
});

// ---------------------------------------------------------------- the queue

describe("the persisted event queue", () => {
  test("it flushes on its interval while the tab is visible", async () => {
    const h = harness();
    const c = h.client({ store: false, pollInterval: 3_600_000, flushInterval: 5_000, sendSummaries: false });
    await h.serve("/sdk/v1/evaluate", values(1, {}));
    c.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    await h.advance(5_000);
    expect(h.net.waiting("/sdk/v1/events")).toHaveLength(1);
  });

  test("its interval waits while the tab is hidden, the queue staying in storage", async () => {
    const h = harness();
    const store = new MemoryStore();
    const c = h.client({ store, pollInterval: 3_600_000, flushInterval: 5_000, sendSummaries: false });
    await h.serve("/sdk/v1/evaluate", values(1, {}));
    h.page.hide();
    c.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    await h.advance(60_000);
    expect(h.net.waiting("/sdk/v1/events"), "a hidden tab does not flush on its interval").toHaveLength(0);
    expect(store.getItem(keys.events)).not.toBeNull();
    h.page.show();
    await h.serve("/sdk/v1/evaluate", values(1, {}));
    await h.advance(5_000);
    expect(h.net.waiting("/sdk/v1/events")).toHaveLength(1);
  });

  test("it is bounded and drops its oldest events", async () => {
    const h = harness();
    const c = h.client({ store: false, pollInterval: 3_600_000, flushInterval: 3_600_000, sendSummaries: false }, key, { capacity: 3 });
    await h.serve("/sdk/v1/evaluate", values(1, {}));
    for (let i = 0; i < 5; i++) {
      c.alias({ fromUnitType: "anonymous", fromUnitId: `d-${i}`, toUnitType: "user", toUnitId: "u-1001" });
    }
    expect(h.count("queue_dropped")).toBe(2);
    void c.flush();
    const req = await h.waitFor("/sdk/v1/events");
    const sent = (JSON.parse(req.body ?? "{}") as { events: { from_unit_id: string }[] }).events;
    expect(sent.map((e) => e.from_unit_id)).toStrictEqual(["d-2", "d-3", "d-4"]);
    req.answer({ status: 202, body: JSON.stringify({ accepted: 3, duplicates: 0, dropped_rows: 0, rejected: [] }) });
  });

  test("a queue that outlived the tab is flushed by the next client with the same event IDs", async () => {
    const h = harness();
    const store = new MemoryStore();
    const first = h.client({ store, pollInterval: 3_600_000, flushInterval: 3_600_000, sendSummaries: false });
    await h.serve("/sdk/v1/evaluate", values(1, {}));
    first.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    void first.flush();
    const failed = await h.waitFor("/sdk/v1/events");
    const ids = (JSON.parse(failed.body ?? "{}") as { events: { event_id: string }[] }).events.map((e) => e.event_id);
    failed.answer({ status: 500 });
    await settle();
    expect(store.getItem(keys.events), "the queue survives the tab").not.toBeNull();
    await first.close();

    const second = h.client({ store, pollInterval: 3_600_000, flushInterval: 3_600_000, sendSummaries: false });
    await h.serve("/sdk/v1/evaluate", values(1, {}));
    void second.flush();
    const retried = await h.waitFor("/sdk/v1/events");
    expect((JSON.parse(retried.body ?? "{}") as { events: { event_id: string }[] }).events.map((e) => e.event_id)).toStrictEqual(ids);
    retried.answer({ status: 202, body: JSON.stringify({ accepted: 1, duplicates: 0, dropped_rows: 0, rejected: [] }) });
    await settle();
    expect(store.getItem(keys.events), "an accepted batch leaves nothing behind").toBeNull();
  });

  test("tabs sharing a key keep each other's unsent events, and a batch removes only its own", async () => {
    const h = harness();
    const store = new MemoryStore();
    const options = { store, pollInterval: 3_600_000, flushInterval: 3_600_000, sendSummaries: false };
    const a = h.client(options);
    const b = h.client(options);
    await h.serve("/sdk/v1/evaluate", values(1, {}));
    await h.serve("/sdk/v1/evaluate", values(1, {}));
    // A millisecond apart, so that each event gets an ID of its own.
    a.alias({ fromUnitType: "anonymous", fromUnitId: "d-a1", toUnitType: "user", toUnitId: "u-1001" });
    h.tick();
    b.alias({ fromUnitType: "anonymous", fromUnitId: "d-b1", toUnitType: "user", toUnitId: "u-1001" });
    void b.flush();
    const req = await h.waitFor("/sdk/v1/events");
    expect(unitIds(req), "the other tab's event goes too").toStrictEqual(["d-a1", "d-b1"]);
    h.tick();
    a.alias({ fromUnitType: "anonymous", fromUnitId: "d-a2", toUnitType: "user", toUnitId: "u-1001" });
    req.answer({ status: 202, body: JSON.stringify({ accepted: 2, duplicates: 0, dropped_rows: 0, rejected: [] }) });
    await settle();
    void a.flush();
    expect(unitIds(await h.waitFor("/sdk/v1/events")), "recorded while the batch was in flight").toStrictEqual(["d-a2"]);
  });

  test("an exposure reports the assignment served, not the document that arrived since", async () => {
    const h = harness();
    const c = h.client({ store: false, pollInterval: 3_600_000, flushInterval: 3_600_000, sendSummaries: false });
    await h.serve("/sdk/v1/evaluate", values(1, { manual: experimentFlag(true, false, "paywall_exp1") }));
    // The application reads the flag and acts on what it was served.
    expect(c.getBool("manual", false)).toBe(true);

    // The experiment is stopped before the unit reaches the screen the
    // variation decides; the assignment it acted on is still the one to
    // report, with the ruleset version it was served under.
    h.page.hide();
    await h.advance(10_000);
    h.page.show();
    await h.serve("/sdk/v1/evaluate", values(2, { manual: boolFlag(false) }));
    expect(c.status().rulesetVersion).toBe(2);
    c.exposure("manual");
    void c.flush();

    const req = await h.waitFor("/sdk/v1/events");
    const sent = (JSON.parse(req.body ?? "{}") as { events: Record<string, unknown>[] }).events;
    expect(sent).toHaveLength(1);
    expect(sent[0]).toMatchObject({ flag_key: "manual", experiment_key: "paywall_exp1", variation_key: "treatment", ruleset_version: 1 });
    req.answer({ status: 202, body: JSON.stringify({ accepted: 1, duplicates: 0, dropped_rows: 0, rejected: [] }) });

    // Reading it again under the new document forgets the assignment: the
    // call now reports nothing rather than an experiment nobody serves.
    expect(c.getBool("manual", true)).toBe(false);
    c.exposure("manual");
    expect(h.count("exposure_skipped")).toBe(1);
  });

  test("an exposure is emitted once per assignment, automatically for a flag that asks for it", async () => {
    const h = harness();
    const c = h.client({ store: false, pollInterval: 3_600_000, flushInterval: 3_600_000, sendSummaries: false });
    await h.serve("/sdk/v1/evaluate", values(1, {
      auto: experimentFlag(true, true),
      manual: experimentFlag(true, false, "paywall_exp1"),
      plain: boolFlag(true),
    }));

    // An auto-exposure flag emits on evaluation, once per assignment.
    expect(c.getBool("auto", false)).toBe(true);
    expect(c.getBool("auto", false)).toBe(true);
    // An exposure reports the detail the flag was served with, so
    // the value is read where it is used and reported where it is seen.
    expect(c.getBool("manual", false)).toBe(true);
    c.exposure("manual");
    c.exposure("manual");
    // A flag with no experiment block emits nothing; nor does one that was
    // never evaluated.
    expect(c.getBool("plain", false)).toBe(true);
    c.exposure("plain");
    c.exposure("never_read");
    void c.flush();
    const req = await h.waitFor("/sdk/v1/events");
    const sent = (JSON.parse(req.body ?? "{}") as { events: Record<string, unknown>[] }).events;
    expect(sent.map((e) => e.flag_key)).toStrictEqual(["auto", "manual"]);
    expect(sent[0]).toMatchObject({
      type: "exposure",
      experiment_key: "checkout_v2_exp1",
      variation_key: "treatment",
      unit_type: "anonymous",
      unit_id: "d-8f1c",
      evaluation_reason: "experiment",
      ruleset_version: 1,
      override: false,
    });
    req.answer({ status: 202, body: JSON.stringify({ accepted: 2, duplicates: 0, dropped_rows: 0, rejected: [] }) });
  });

  test("a local override serves local_override, emits no exposure and is counted in the summary", async () => {
    const h = harness();
    const c = h.client({ store: false, pollInterval: 3_600_000, flushInterval: 3_600_000, allowOverrides: true });
    await h.serve("/sdk/v1/evaluate", values(1, { auto: experimentFlag(true, true) }));
    c.overrides.set("auto", false);
    expect(c.getDetail("auto", true)).toMatchObject({ value: false, reason: "local_override" });
    expect(c.overrides.list()).toStrictEqual({ auto: false });
    c.exposure("auto");
    void c.flush();
    const req = await h.waitFor("/sdk/v1/events");
    const sent = (JSON.parse(req.body ?? "{}") as { events: Record<string, unknown>[] }).events;
    expect(sent.map((e) => e.type)).toStrictEqual(["evaluation_summary"]);
    expect(sent[0]?.counts).toStrictEqual([{ flag: "auto", variation: null, reason: "local_override", count: 1 }]);
    req.answer({ status: 202, body: JSON.stringify({ accepted: 1, duplicates: 0, dropped_rows: 0, rejected: [] }) });

    c.overrides.remove("auto");
    expect(c.getDetail("auto", false)).toMatchObject({ value: true, reason: "experiment" });
    c.overrides.clear();
    expect(c.overrides.list()).toStrictEqual({});
  });

  test("local overrides need the option", async () => {
    const h = harness();
    const c = h.client({ store: false, pollInterval: 3_600_000 });
    await h.serve("/sdk/v1/evaluate", values(1, { checkout: boolFlag(true) }));
    expect(() => { c.overrides.set("checkout", false); }).toThrow(LocalOverridesNotAllowed);
    expect(h.count("override_refused")).toBe(1);
    expect(c.getBool("checkout", false)).toBe(true);
  });
});

// ---------------------------------------------------------------- offline

describe("the beacon of a hidden or closing page", () => {
  const link = (id: string) => ({ fromUnitType: "anonymous", fromUnitId: id, toUnitType: "user", toUnitId: "u-1001" });
  const options = { pollInterval: 3_600_000, flushInterval: 3_600_000, sendSummaries: false };

  test("a batch in flight is left to its own request", async () => {
    const h = harness();
    const store = new MemoryStore();
    const c = h.client({ ...options, store });
    await h.serve("/sdk/v1/evaluate", values(1, {}));
    c.alias(link("d-1"));
    void c.flush();
    const req = await h.waitFor("/sdk/v1/events");
    h.tick();
    c.alias(link("d-2"));
    h.page.hide();
    const beacons = h.net.waiting("/sdk/v1/events").filter((p) => p.kind === "beacon");
    expect(beacons.map(unitIds)).toStrictEqual([["d-2"]]);
    req.answer({ status: 202, body: JSON.stringify({ accepted: 1, duplicates: 0, dropped_rows: 0, rejected: [] }) });
    await settle();
    expect(store.getItem(keys.events), "both left the queue").toBeNull();
    for (const b of beacons) b.answer({ status: 202 });
  });

  test("the beacons of one hide stay within the page's quota, the rest staying queued", async () => {
    const h = harness();
    const store = new MemoryStore();
    const c = h.client({ ...options, store });
    await h.serve("/sdk/v1/evaluate", values(1, {}));
    // About 1.7 KiB each, fewer than a batch: 90 of them are over twice the quota.
    const id = (i: number): string => `d-${String(i).padStart(4, "0")}-${"x".repeat(1_500)}`;
    for (let i = 0; i < 90; i++) {
      c.alias(link(id(i)));
      h.tick();
    }
    h.page.hide();
    const beacons = h.net.waiting("/sdk/v1/events");
    expect(beacons.length).toBeGreaterThan(0);
    expect(beacons.every((p) => p.kind === "beacon")).toBe(true);
    expect(beacons.every((p) => unitIds(p).length <= 100), "at most a batch each").toBe(true);
    const bytes = beacons.reduce((n, p) => n + new TextEncoder().encode(p.body ?? "").length, 0);
    expect(bytes).toBeLessThanOrEqual(64 * 1024);
    expect(bytes, "the quota is used, not wasted").toBeGreaterThan(60 * 1024);
    const sent = beacons.flatMap(unitIds);
    const left = (JSON.parse(store.getItem(keys.events) ?? "[]") as { from_unit_id: string }[]).map((e) => e.from_unit_id);
    expect(sent.length + left.length).toBe(90);
    expect(left[0], "the oldest go first").toBe(id(sent.length));
    expect(h.count("events_failed")).toBe(1);
    for (const b of beacons) b.answer({ status: 202 });
  });

  test("without a beacon, or when the browser refuses it, a hidden page flushes by request", async () => {
    for (const beacon of [null, () => false]) {
      const h = harness();
      h.beacon = beacon;
      const c = h.client({ ...options, store: false });
      await h.serve("/sdk/v1/evaluate", values(1, {}));
      c.alias(link("d-1"));
      h.page.hide();
      const req = await h.waitFor("/sdk/v1/events", 0);
      expect(req.kind).toBe("fetch");
      expect(req.headers.authorization).toBe(`Bearer ${key}`);
      req.answer({ status: 202, body: JSON.stringify({ accepted: 1, duplicates: 0, dropped_rows: 0, rejected: [] }) });
      await settle();
    }
  });

  test("a closed client sends no beacon", async () => {
    const h = harness();
    const c = h.client({ ...options, store: false });
    await h.serve("/sdk/v1/evaluate", values(1, {}));
    const closing = c.close();
    await h.advance(10_000);
    await closing;
    c.alias(link("d-1"));
    h.page.hide();
    expect(h.net.waiting("/sdk/v1/events")).toHaveLength(0);
  });
});

describe("offline mode", () => {
  test("it makes no request, records events in memory and rewrites values in place", async () => {
    const h = harness();
    const c = h.client({ offline: values(4, { checkout: boolFlag(true) }) });
    expect(c.stage).toBe("fresh");
    expect(await c.ready(0)).toBe("fresh");
    expect(c.getBool("checkout", false)).toBe(true);
    c.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    await c.flush();
    await h.advance(60_000);
    expect(h.net.seen, "an offline client never reaches the network").toHaveLength(0);
    expect(c.offline?.recorded().map((e) => e.type)).toStrictEqual(["alias"]);

    c.offline?.set("checkout", false);
    expect(c.getBool("checkout", true)).toBe(false);
    expect(c.getDetail("checkout", true).reason).toBe("targeting_match");
    c.offline?.reload();
    expect(c.getBool("checkout", false)).toBe(true);
  });

  test("a server key is refused even offline: it must never ship in a bundle", () => {
    const h = harness();
    expect(() => h.client({ offline: values(1, {}) }, serverKey)).toThrow(/client key/);
  });
});

// ---------------------------------------------------------------- totality

describe("every public member is total", () => {
  test("a throwing listener and a throwing logger never reach the caller", async () => {
    const h = harness();
    const c = h.client({ store: false, logger: () => { throw new Error("the logger is the caller's code"); } });
    c.onChange(() => { throw new Error("so is a listener"); });
    c.onStage(() => { throw new Error("and so is this one"); });
    await h.serve("/sdk/v1/evaluate", values(1, { checkout: boolFlag(true) }));
    expect(c.getBool("checkout", false)).toBe(true);
  });

  test("a getter of the wrong type serves its default and logs the mismatch once", async () => {
    const h = harness();
    const c = h.client({ store: false, pollInterval: 3_600_000 });
    await h.serve("/sdk/v1/evaluate", values(1, { checkout: boolFlag(true) }));
    expect(c.getString("checkout", "none")).toBe("none");
    expect(c.getString("checkout", "none")).toBe("none");
    expect(h.count("type_mismatch")).toBe(1);
    expect(c.getDetail("checkout", "none")).toMatchObject({ reason: "fallback", errorCode: "type_mismatch" });
  });

  test("a JSON getter serves a value the caller may keep and mutate", async () => {
    const h = harness();
    const c = h.client({ store: false, pollInterval: 3_600_000 });
    await h.serve("/sdk/v1/evaluate", values(1, { theme: { type: "json", value: { mode: "dark" }, variation: "dark", reason: "targeting_match" } }));
    const theme = c.getJson("theme", { mode: "system" }) as { mode: string };
    theme.mode = "light";
    expect(c.getJson("theme", { mode: "system" })).toStrictEqual({ mode: "dark" });
  });

  test("close stops the timers and leaves the values serving", async () => {
    const h = harness();
    const c = h.client({ store: false, pollInterval: 30_000 });
    await h.serve("/sdk/v1/evaluate", values(1, { checkout: boolFlag(true) }));
    await c.close();
    await h.advance(600_000);
    expect(h.net.waiting("/sdk/v1/evaluate")).toHaveLength(0);
    expect(c.getBool("checkout", false)).toBe(true);
    expect(await c.ready(0)).toBe("fresh");
  });
});
