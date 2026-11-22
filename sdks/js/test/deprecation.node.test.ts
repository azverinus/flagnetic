// The deprecation log: an operation the server has deprecated answers with a
// `Deprecation` header (and `Sunset` when dated); the SDK writes one warning
// per operation, with the sunset, and the call carries on as if the header
// were not there. Server mode runs against a mock platform on a socket, the
// browser client against a mocked `fetch`.
import { afterEach, describe, expect, test, vi } from "vitest";
import { createClientWith } from "../src/client/client.js";
import { createServerWith } from "../src/server/client.js";
import type { Timing } from "../src/server/delivery.js";
import { MockNetwork } from "./support/mock-network.js";
import { MockPlatform, rulesetOf } from "./support/mock-platform.js";
import { VirtualClock } from "./support/virtual-clock.js";

const serverKey = "fn_srv_0123456789abcdefghijklmnopqrstuvwxyzABCD";
const clientKey = "fn_cli_0123456789abcdefghijklmnopqrstuvwxyzABCD";

const fast: Partial<Timing> = { requestTimeout: 500, pollConnected: 10_000, pollDisconnected: 10_000, retryInitial: 40, retryMax: 80, streamRetryInitial: 40 };
const deprecated = { Deprecation: "@1767225600", Sunset: "Thu, 31 Dec 2026 23:59:59 GMT" };

type Line = { level: string; event: string; fields: Readonly<Record<string, unknown>> };

const cleanups: (() => Promise<void>)[] = [];
afterEach(async () => {
  for (const c of cleanups.splice(0).reverse()) await c();
});

const warnings = (logs: Line[]): Line[] => logs.filter((l) => l.event === "operation_deprecated");

describe("server mode", () => {
  async function start(headers: Record<string, string>, stream = true) {
    const p = await MockPlatform.start(rulesetOf(1, { shiny: true }));
    p.responseHeaders = headers;
    cleanups.push(() => p.stop());
    const logs: Line[] = [];
    const client = createServerWith(serverKey, p.baseUrl, { stream, logger: (level, event, fields) => logs.push({ level, event, fields }) }, { timing: fast });
    cleanups.push(() => client.close());
    return { p, client, logs };
  }

  test("warns once per operation with the sunset, and serves as before", async () => {
    const { p, client, logs } = await start(deprecated);
    await client.ready(2000);
    await vi.waitFor(() => { expect(p.openStreams).toBe(1); });
    // Two more answers of the ruleset operation, and two batches of events.
    p.publish(rulesetOf(2, { shiny: false }));
    await vi.waitFor(() => { expect(p.rulesetRequests().length).toBeGreaterThanOrEqual(2); });
    await vi.waitFor(() => { expect(client.getBool({ units: { user: "u" } }, "shiny", true)).toBe(false); });
    client.exposure({ units: { user: "u" } }, "shiny");
    await client.flush();
    client.exposure({ units: { user: "u2" } }, "shiny");
    await client.flush();

    const found = warnings(logs);
    expect(found.map((l) => l.fields.operation).sort()).toStrictEqual(["events", "ruleset", "stream"]);
    for (const l of found) {
      expect(l.level).toBe("warn");
      expect(l.fields).toMatchObject({ deprecation: deprecated.Deprecation, sunset: deprecated.Sunset });
    }
  });

  test("leaves the sunset out when the server sets none", async () => {
    const { client, logs } = await start({ Deprecation: "@1767225600" }, false);
    await client.ready(2000);
    const [line] = warnings(logs);
    expect(line?.fields).toStrictEqual({ operation: "ruleset", deprecation: "@1767225600" });
  });

  test("an answer that fails still reports, and the call fails for its own reason", async () => {
    const p = await MockPlatform.start(rulesetOf(1, { shiny: true }));
    p.responseHeaders = deprecated;
    p.rulesetReplies.push({ kind: "status", status: 503 });
    cleanups.push(() => p.stop());
    const logs: Line[] = [];
    const client = createServerWith(serverKey, p.baseUrl, { stream: false, logger: (level, event, fields) => logs.push({ level, event, fields }) }, { timing: fast });
    cleanups.push(() => client.close());
    await vi.waitFor(() => { expect(logs.some((l) => l.event === "fetch_failed")).toBe(true); });
    expect(warnings(logs)).toHaveLength(1);
    await client.ready(2000);
    expect(client.getBool({ units: { user: "u" } }, "shiny", false)).toBe(true);
  });

  test("says nothing when no operation is deprecated", async () => {
    const { client, logs } = await start({});
    await client.ready(2000);
    await client.flush();
    expect(warnings(logs)).toStrictEqual([]);
  });

  test("a throwing logger does not fail the call", async () => {
    const p = await MockPlatform.start(rulesetOf(1, { shiny: true }));
    p.responseHeaders = deprecated;
    cleanups.push(() => p.stop());
    const client = createServerWith(serverKey, p.baseUrl, { stream: false, logger: () => { throw new Error("logger broke"); } }, { timing: fast });
    cleanups.push(() => client.close());
    expect(await client.ready(2000)).toBe("fresh");
  });
});

describe("browser client", () => {
  const answer = JSON.stringify({
    schema_version: 1,
    ruleset_version: 1,
    environment: { key: "development", protected: false },
    flags: { shiny: { type: "boolean", value: true, variation: "true", reason: "targeting_match", override_influenced: false, automatic_exposure: false } },
  });

  test("warns once per operation with the sunset, and serves the answer", async () => {
    const clock = new VirtualClock();
    const net = new MockNetwork(clock);
    const logs: Line[] = [];
    const client = createClientWith(
      clientKey,
      "http://flagnetic.test",
      { context: { units: { user: "u" } }, store: false, logger: (level, event, fields) => logs.push({ level, event, fields }) },
      { fetch: net.fetch, clock },
    );
    // Closing waits on the virtual clock for the flush; the test ends instead.
    cleanups.push(() => { void client.close(); return Promise.resolve(); });
    const evaluate = async (): Promise<void> => {
      await vi.waitFor(() => { expect(net.waiting("/sdk/v1/evaluate")).toHaveLength(1); });
      net.waiting("/sdk/v1/evaluate")[0]?.answer({ status: 200, headers: deprecated, body: answer });
    };
    await evaluate();
    await vi.waitFor(() => { expect(client.getBool("shiny", false)).toBe(true); });
    void client.setContext({ units: { user: "u2" } });
    await evaluate();
    await vi.waitFor(() => { expect(net.seen.filter((r) => r.path === "/sdk/v1/evaluate")).toHaveLength(2); });

    const found = warnings(logs);
    expect(found).toHaveLength(1);
    expect(found[0]).toMatchObject({ level: "warn", fields: { operation: "evaluate", deprecation: deprecated.Deprecation, sunset: deprecated.Sunset } });
  });
});
