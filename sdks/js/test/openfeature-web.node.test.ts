// FlagneticWebProvider registered with the real @openfeature/web-sdk
// against an evaluation endpoint on Node's http server: provider events and
// OpenFeature's context changes, which reach the endpoint as new contexts.
import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";
import { OpenFeature, ProviderEvents, type EvaluationContext, type EventDetails } from "@openfeature/web-sdk";
import { afterEach, describe, expect, test } from "vitest";
import { FlagneticWebProvider } from "../src/openfeature.js";
import type { LogEvent } from "../src/client.js";
import { valuesFor } from "./support/flags-fixture.js";

const key = "fn_cli_0123456789abcdefghijklmnopqrstuvwxyzABCD";

interface Posted {
  units?: Record<string, string>;
  attributes?: Record<string, unknown>;
}

/** An evaluation endpoint serving checkout to Germany, or answering with status while it is set. */
class Endpoint {
  readonly contexts: Posted[] = [];
  status = 200;
  baseUrl = "";
  private readonly server = createServer((req, res) => {
    this.handle(req, res);
  });

  async start(): Promise<this> {
    await new Promise<void>((resolve) => this.server.listen(0, "127.0.0.1", resolve));
    this.baseUrl = `http://127.0.0.1:${(this.server.address() as AddressInfo).port}`;
    return this;
  }

  async stop(): Promise<void> {
    this.server.closeAllConnections();
    await new Promise<void>((resolve) => this.server.close(() => { resolve(); }));
  }

  private handle(req: IncomingMessage, res: ServerResponse): void {
    if (req.method !== "POST" || req.url !== "/sdk/v1/evaluate") {
      res.writeHead(404).end();
      return;
    }
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => {
      const { context } = JSON.parse(Buffer.concat(chunks).toString("utf8")) as { context: Posted };
      this.contexts.push(context);
      if (this.status !== 200) {
        res.writeHead(this.status).end();
        return;
      }
      const doc = valuesFor({ checkout: context.attributes?.["country"] === "DE" });
      res.writeHead(200, { "Content-Type": "application/json" }).end(JSON.stringify(doc));
    });
  }
}

const cleanups: (() => Promise<void>)[] = [];
afterEach(async () => {
  for (const c of cleanups.splice(0).reverse()) await c();
  await OpenFeature.clearProviders();
  OpenFeature.clearHandlers();
});

async function endpoint(): Promise<Endpoint> {
  const e = await new Endpoint().start();
  cleanups.push(() => e.stop());
  return e;
}

async function register(e: Endpoint, context: EvaluationContext, logger: (event: LogEvent) => void = () => undefined) {
  const provider = new FlagneticWebProvider(key, e.baseUrl, { store: false, readyTimeout: 1_000, logger: (_level, event) => { logger(event); } });
  cleanups.push(() => provider.onClose());
  await OpenFeature.setProviderAndWait(provider, context);
  return { provider, client: OpenFeature.getClient() };
}

function next(type: ProviderEvents): Promise<EventDetails | undefined> {
  return new Promise((resolve) => {
    const handler = (details?: EventDetails): void => {
      OpenFeature.removeHandler(type, handler);
      resolve(details);
    };
    OpenFeature.addHandler(type, handler);
  });
}

/** Records what OpenFeature announces of these types, in order; a status event already matching runs at once. */
function record(...types: ProviderEvents[]): ProviderEvents[] {
  const seen: ProviderEvents[] = [];
  for (const type of types) {
    OpenFeature.addHandler(type, () => {
      seen.push(type);
    });
  }
  return seen;
}

/** Records every event the provider itself sends, in order. */
function sent(provider: FlagneticWebProvider): string[] {
  const seen: string[] = [];
  for (const type of Object.values(ProviderEvents)) {
    provider.events.addHandler(type, () => {
      seen.push(type);
    });
  }
  return seen;
}

const settled = (ms = 50): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));

describe("initialize", () => {
  test("READY once values for OpenFeature's context arrived", async () => {
    const e = await endpoint();
    const { provider, client } = await register(e, { targetingKey: "u-1", country: "DE" });
    expect(provider.client.stage).toBe("fresh");
    expect(client.providerStatus).toBe("READY");
    expect(client.getBooleanDetails("checkout", false)).toMatchObject({ value: true, reason: "TARGETING_MATCH" });
    // The provider is built before OpenFeature hands it a context: nothing
    // is evaluated for the empty one meanwhile.
    expect(e.contexts).toHaveLength(1);
    expect(e.contexts[0]).toMatchObject({ units: { user: "u-1" }, attributes: { country: "DE" } });
  });

  test("a key revoked before initialize resolved: ERROR right after OpenFeature's READY, and the default served", async () => {
    const e = await endpoint();
    e.status = 401;
    const errored = next(ProviderEvents.Error);
    const { client } = await register(e, { targetingKey: "u-1" });
    expect((await errored)?.errorCode).toBe("GENERAL");
    expect(client.providerStatus).toBe("ERROR");
    expect(client.getBooleanValue("checkout", true)).toBe(true);
  });

  test("the logger option keeps receiving every log event", async () => {
    const e = await endpoint();
    const events: LogEvent[] = [];
    await register(e, { targetingKey: "u-1" }, (event) => events.push(event));
    expect(events.length).toBeGreaterThan(0);
  });
});

describe("context changes", () => {
  test("OpenFeature's new context is evaluated, resolution follows, and only the change's own events are sent", async () => {
    const e = await endpoint();
    const { provider, client } = await register(e, { targetingKey: "u-1", country: "DE" });
    const announced = record(ProviderEvents.Reconciling, ProviderEvents.ContextChanged);
    const own = sent(provider);
    await OpenFeature.setContext({ targetingKey: "u-2", country: "FR", units: { anonymous: "d-1" } });
    await settled();
    expect(e.contexts.at(-1)).toMatchObject({ units: { anonymous: "d-1", user: "u-2" }, attributes: { country: "FR" } });
    expect(client.getBooleanDetails("checkout", true)).toMatchObject({ value: false, reason: "TARGETING_MATCH" });
    expect(announced).toStrictEqual([ProviderEvents.Reconciling, ProviderEvents.ContextChanged]);
    // The change's values arriving moved the client back to fresh, and the
    // previous ones served as last_good meanwhile: neither is news.
    expect(own).toStrictEqual([ProviderEvents.ConfigurationChanged]);
    expect(client.providerStatus).toBe("READY");
  });

  test("a change whose fetch fails ends STALE, the previous values serving; the next one that succeeds is READY", async () => {
    const e = await endpoint();
    const { client } = await register(e, { targetingKey: "u-1", country: "DE" });
    e.status = 500;
    const stale = next(ProviderEvents.Stale);
    await OpenFeature.setContext({ targetingKey: "u-2", country: "FR" });
    await stale;
    expect(client.providerStatus).toBe("STALE");
    expect(client.getBooleanValue("checkout", false)).toBe(true);

    e.status = 200;
    await OpenFeature.setContext({ targetingKey: "u-3", country: "FR" });
    await settled();
    expect(client.providerStatus).toBe("READY");
    expect(client.getBooleanValue("checkout", true)).toBe(false);
  });

  test("a key revoked during a change ends ERROR", async () => {
    const e = await endpoint();
    const { client } = await register(e, { targetingKey: "u-1", country: "DE" });
    e.status = 401;
    const errored = next(ProviderEvents.Error);
    await OpenFeature.setContext({ targetingKey: "u-2" });
    await errored;
    await settled();
    expect(client.providerStatus).toBe("ERROR");
  });
});

describe("closing", () => {
  test("no event is sent after the provider is closed", async () => {
    const e = await endpoint();
    const { provider } = await register(e, { targetingKey: "u-1", country: "DE" });
    const own = sent(provider);
    await provider.onClose();
    await OpenFeature.setContext({ targetingKey: "u-2" });
    await settled();
    expect(own).toStrictEqual([]);
  });
});
