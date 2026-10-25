// The browser client in Chromium, Firefox and WebKit, on real time, against
// a server on another origin (test/support/cross-origin-server.ts): what a
// mock of `fetch` cannot show. The browser preflights every request, so
// the client may send only the headers the server allows; `localStorage`
// is the real one, and so is its absence; the stream is a real streamed
// response. A page closed in a tab of its own hands its last events to a
// real beacon.
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { commands } from "vitest/browser";
import { anonymousId, createClient, type ClientOptions, type FlagneticClient } from "../src/client.js";

interface SeenRequest {
  readonly method: string;
  readonly path: string;
  readonly query: string;
  readonly headers: Record<string, string>;
  readonly body: string;
}

declare module "vitest/browser" {
  interface BrowserCommands {
    crossOriginStart(): Promise<string>;
    crossOriginReset(): Promise<void>;
    crossOriginServe(sdkKey: string, doc: unknown): Promise<void>;
    crossOriginRequests(): Promise<SeenRequest[]>;
    crossOriginSend(text: string): Promise<number>;
    closePage(baseUrl: string, sdkKey: string): Promise<void>;
  }
}

const key = "fn_cli_0123456789abcdefghijklmnopqrstuvwxyzABCD";
const otherKey = "fn_cli_9876543210zyxwvutsrqponmlkjihgfedcbaZYXW";
const context = { units: { anonymous: "d-8f1c" }, attributes: { country: "DE" } };

function values(version: number, checkout: boolean): Record<string, unknown> {
  return {
    schema_version: 1,
    ruleset_version: version,
    environment: { key: "production", protected: true },
    flags: {
      checkout: { type: "boolean", value: checkout, variation: String(checkout), reason: "targeting_match", override_influenced: false, automatic_exposure: false },
    },
  };
}

let baseUrl = "";
const clients: FlagneticClient[] = [];

function client(options: ClientOptions = {}, sdkKey = key): FlagneticClient {
  const c = createClient(sdkKey, baseUrl, { context, logger: () => undefined, ...options });
  clients.push(c);
  return c;
}

async function requests(path: string, method?: string): Promise<SeenRequest[]> {
  return (await commands.crossOriginRequests()).filter((r) => r.path === path && (method === undefined || r.method === method));
}

async function until(predicate: () => boolean | Promise<boolean>, what: string, timeout = 10_000): Promise<void> {
  const deadline = Date.now() + timeout;
  while (!(await predicate())) {
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
}

beforeEach(async () => {
  baseUrl = await commands.crossOriginStart();
  await commands.crossOriginReset();
  localStorage.clear();
});

afterEach(async () => {
  for (const c of clients.splice(0)) await c.close();
  localStorage.clear();
});

describe("a page on another origin", () => {
  test("gets the values for its context, naming its SDK in the context and sending only headers the preflight allows", async () => {
    expect(new URL(baseUrl).origin, "the server is cross-origin").not.toBe(location.origin);
    await commands.crossOriginServe(key, values(1, true));
    const c = client({ appVersion: "1.42.0" });
    expect(await c.ready(5_000)).toBe("fresh");
    expect(c.getDetail("checkout", false)).toMatchObject({ value: true, reason: "targeting_match", rulesetVersion: 1 });

    const preflight = await requests("/sdk/v1/evaluate", "OPTIONS");
    expect(preflight.length, "the browser preflighted the request").toBeGreaterThan(0);
    const [evaluate] = await requests("/sdk/v1/evaluate", "POST");
    expect(evaluate?.headers.authorization).toBe(`Bearer ${key}`);
    expect(evaluate?.headers["flagnetic-schema-version"]).toBe("1");
    expect(JSON.parse(evaluate?.body ?? "{}")).toStrictEqual({
      context: {
        units: { anonymous: "d-8f1c" },
        attributes: { platform: "browser", app_version: "1.42.0", sdk: "flagnetic-js", sdk_version: "0.0.0", country: "DE" },
      },
    });
  });

  test("keeps the last good values per key in localStorage, served before any request on the next load", async () => {
    await commands.crossOriginServe(key, values(3, true));
    const first = client();
    expect(await first.ready(5_000)).toBe("fresh");
    await first.close();

    // The server is gone for this key; the next page load still serves.
    await commands.crossOriginReset();
    const second = client();
    expect(second.stage).toBe("last_good");
    expect(second.getBool("checkout", false)).toBe(true);
    expect(second.status().rulesetVersion).toBe(3);

    // Another key in the same browser learns nothing from it.
    const other = client({}, otherKey);
    expect(other.stage).toBe("built_in");
    expect(other.getBool("checkout", false)).toBe(false);
  });

  test("keeps its values in memory when localStorage cannot be reached", async () => {
    const own = Object.getOwnPropertyDescriptor(window, "localStorage");
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      get() {
        throw new DOMException("The operation is insecure.", "SecurityError");
      },
    });
    try {
      await commands.crossOriginServe(key, values(1, true));
      const c = client();
      expect(await c.ready(5_000)).toBe("fresh");
      expect(c.getBool("checkout", false)).toBe(true);
      // Nothing persisted, so the next client starts from its defaults.
      await commands.crossOriginReset();
      expect(client().stage).toBe("built_in");
    } finally {
      if (own) Object.defineProperty(window, "localStorage", own);
      else delete (window as { localStorage?: unknown }).localStorage;
    }
  });

  test("setContext resolves once the new context's values arrived", async () => {
    await commands.crossOriginServe(key, values(1, false));
    const c = client();
    expect(await c.ready(5_000)).toBe("fresh");

    await commands.crossOriginServe(key, values(2, true));
    expect(await c.setContext({ units: { anonymous: "d-8f1c", user: "u-1001" } })).toBe("fresh");
    expect(c.getBool("checkout", false)).toBe(true);
    const posted = await requests("/sdk/v1/evaluate", "POST");
    expect((JSON.parse(posted.at(-1)?.body ?? "{}") as { context: { units: unknown } }).context.units).toStrictEqual({
      anonymous: "d-8f1c",
      user: "u-1001",
    });
  });

  test("holds the stream over fetch when asked to and refetches on a change", async () => {
    await commands.crossOriginServe(key, values(1, false));
    const c = client({ stream: true });
    expect(await c.ready(5_000)).toBe("fresh");
    const [stream] = await requests("/sdk/v1/stream", "GET");
    expect(stream?.query, "the key never travels in the URL").toBe("");
    expect(stream?.headers.authorization).toBe(`Bearer ${key}`);
    expect(stream?.headers["last-event-id"]).toBeUndefined();

    await commands.crossOriginServe(key, values(2, true));
    expect(await commands.crossOriginSend('event: ruleset\nid: 2\ndata: {"version":2}\n\n')).toBe(1);
    await until(() => c.getBool("checkout", false), "the change to arrive");
    expect(c.status().rulesetVersion).toBe(2);
  });

  test("sends its events cross-origin with its SDK in the envelope", async () => {
    await commands.crossOriginServe(key, values(1, true));
    const c = client({ sendSummaries: false });
    expect(await c.ready(5_000)).toBe("fresh");
    c.alias({ fromUnitType: "anonymous", fromUnitId: "d-8f1c", toUnitType: "user", toUnitId: "u-1001" });
    await c.flush();
    const [events] = await requests("/sdk/v1/events", "POST");
    const body = JSON.parse(events?.body ?? "{}") as { sdk: unknown; events: { type: string }[] };
    expect(body.sdk).toStrictEqual({ name: "flagnetic-js", version: "0.0.0", platform: "browser" });
    expect(body.events.map((e) => e.type)).toStrictEqual(["alias"]);
  });

  test("hands its last events to a beacon when the page is closed, with no preflight", async () => {
    await commands.crossOriginServe(key, values(1, true));
    await commands.closePage(baseUrl, key);
    await until(async () => (await requests("/sdk/v1/events", "POST")).length > 0, "the beacon of the closed page");
    const [beacon, ...more] = await requests("/sdk/v1/events", "POST");
    expect(more, "one beacon, nothing sent twice").toHaveLength(0);
    expect(beacon?.query).toBe(`?sdk_key=${key}`);
    expect(beacon?.headers["content-type"]).toMatch(/^text\/plain/);
    expect(beacon?.headers.authorization).toBeUndefined();
    expect(await requests("/sdk/v1/events", "OPTIONS"), "a beacon is a simple request").toHaveLength(0);
    const body = JSON.parse(beacon?.body ?? "{}") as { schema_version: number; sdk: unknown; events: { type: string }[] };
    expect(body.schema_version).toBe(1);
    expect(body.sdk).toStrictEqual({ name: "flagnetic-js", version: "0.0.0", platform: "browser" });
    expect(body.events.map((e) => e.type)).toStrictEqual(["alias"]);
  });
});

describe("anonymousId", () => {
  test("keeps one ID per unit type in localStorage, outside every key's storage", () => {
    const id = anonymousId();
    expect(anonymousId("anonymous")).toBe(id);
    expect(localStorage.getItem("flagnetic.anonymous.anonymous")).toBe(id);
    expect(anonymousId("device")).not.toBe(id);
  });
});
