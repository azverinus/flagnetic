// The server mode's end-to-end smoke against two real replicas, brought up
// by `flagnetic-e2e up` (e2e/README.md): FLAGNETIC_E2E_SEED names the seed
// it wrote, and FLAGNETIC_E2E_CMD the command when it is not on the PATH.
// It runs alone through `pnpm test:e2e`, so it fails rather than skips
// without a seed.
import { execFile } from "node:child_process";
import { readFileSync } from "node:fs";
import { promisify } from "node:util";
import { describe, expect, test } from "vitest";
import { createServer, VERSION, type LogEvent } from "../../src/server.js";
import { eventually, unlike, type InventoryEntry, type Killed, type Seed } from "./seed.js";

const seedPath = process.env.FLAGNETIC_E2E_SEED;
if (!seedPath) throw new Error("FLAGNETIC_E2E_SEED must name the seed `flagnetic-e2e up` wrote");
const seed = JSON.parse(readFileSync(seedPath, "utf8")) as Seed;

async function harness(...args: string[]): Promise<string> {
  const { stdout } = await promisify(execFile)(process.env.FLAGNETIC_E2E_CMD ?? "flagnetic-e2e", args);
  return stdout;
}

// Tests run in order and share the instance: the revocation ends the
// server key, so it comes last.
describe("server mode against two replicas", () => {
  test("ready reaches fresh and every case serves its value with its reason", { timeout: 20_000 }, async () => {
    const client = createServer(seed.server_key, seed.replicas.b);
    try {
      await expect(client.ready(10_000)).resolves.toBe("fresh");
      expect(seed.cases.length).toBeGreaterThan(0);
      for (const c of seed.cases) {
        const d = client.getDetail(c.context, c.flag, unlike(c.value));
        expect({ value: d.value, variation: d.variation, reason: d.reason }, c.name).toStrictEqual({
          value: c.value,
          variation: c.variation,
          reason: c.reason,
        });
      }
    } finally {
      await client.close();
    }
  });

  test("a kill switch through replica A reaches a client streaming from replica B within 2 s", { timeout: 30_000 }, async () => {
    const logs: LogEvent[] = [];
    const client = createServer(seed.server_key, seed.replicas.b, { logger: (_level, event) => logs.push(event) });
    const context = seed.cases.find((c) => c.flag === seed.flags.on)?.context ?? {};
    try {
      await expect(client.ready(10_000)).resolves.toBe("fresh");
      await eventually(() => Promise.resolve(logs.includes("sse_connected")), 10_000, "the stream to replica B never connected");
      expect(client.getBool(context, seed.flags.on, false)).toBe(true);

      const off = new Promise<number>((resolve) => {
        client.onChange((keys) => {
          if (keys.includes(seed.flags.on) && client.getDetail(context, seed.flags.on, true).reason === "off") resolve(Date.now());
        });
      });
      const killed = JSON.parse(await harness("kill", seed.flags.on)) as Killed;
      const timeout = new Promise<number>((resolve) => setTimeout(() => { resolve(Number.POSITIVE_INFINITY); }, 10_000));
      const elapsed = (await Promise.race([off, timeout])) - Date.parse(killed.committed_at);
      // One JSON line, which CI copies to the job summary.
      console.log(JSON.stringify({ kill_switch_ms: elapsed }));
      expect(elapsed).toBeLessThanOrEqual(2_000);
      expect(client.getDetail(context, seed.flags.on, true)).toMatchObject({ value: false, reason: "off" });
    } finally {
      await client.close();
      await harness("restore");
    }
  });

  test("the SDK is in the inventory under its own name", { timeout: 100_000 }, async () => {
    // Each replica writes the SDKs it saw once a minute, so the entry of
    // the requests above appears within 90 seconds.
    await eventually(
      async () => {
        const inventory = JSON.parse(await harness("inventory")) as InventoryEntry[];
        return inventory.some((e) => e.sdk_name === "flagnetic-js" && e.sdk_version === VERSION && e.platform === "node");
      },
      90_000,
      `flagnetic-js ${VERSION} on node never appeared in the inventory`,
    );
  });

  test("a revocation through replica A refuses the key on replica B", { timeout: 30_000 }, async () => {
    await harness("revoke", "server");
    await eventually(
      async () => {
        const client = createServer(seed.server_key, seed.replicas.b, { stream: false });
        try {
          return (await client.ready(2_000)) === "built_in";
        } finally {
          await client.close();
        }
      },
      20_000,
      "replica B kept serving the revoked server key",
    );
  });
});
