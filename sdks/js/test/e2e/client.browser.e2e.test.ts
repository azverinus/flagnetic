// The browser client's end-to-end smoke in Chromium against two real
// replicas, brought up by `flagnetic-e2e up` (e2e/README.md). The page is
// served from the test server's origin and the replicas answer from their
// own, so every request is a real cross-origin one. It runs alone through
// `pnpm test:e2e`, after the server mode's smoke.
import { describe, expect, test } from "vitest";
import { commands } from "vitest/browser";
import { createClient, VERSION } from "../../src/client.js";
import { aliases, eventually, unlike, type EventVolume, type InventoryEntry, type Seed } from "./seed.js";

declare module "vitest/browser" {
  interface BrowserCommands {
    /** The seed `flagnetic-e2e up` wrote. */
    e2eSeed(): Promise<Seed>;
    /** Runs a control command of `flagnetic-e2e` and returns what it printed. */
    e2eHarness(...args: string[]): Promise<string>;
  }
}

const seed = await commands.e2eSeed();

async function aliasesCounted(): Promise<number> {
  return aliases(JSON.parse(await commands.e2eHarness("events")) as EventVolume);
}

// Tests run in order and share the instance: the revocation ends the
// client key, so it comes last. No client keeps values in localStorage,
// so a refused key cannot be answered from an earlier test's.
describe("the browser client against two replicas, from another origin", () => {
  test("ready reaches fresh and every case serves its value with its client reason", { timeout: 30_000 }, async () => {
    expect(new URL(seed.replicas.b).origin, "the replicas answer from another origin than this page").not.toBe(location.origin);
    const client = createClient(seed.client_key, seed.replicas.b, { context: seed.cases[0]?.context ?? {}, store: false });
    try {
      await expect(client.ready(10_000)).resolves.toBe("fresh");
      expect(seed.cases.length).toBeGreaterThan(0);
      for (const c of seed.cases) {
        await expect(client.setContext(c.context), c.name).resolves.toBe("fresh");
        const d = client.getDetail(c.flag, unlike(c.value));
        expect({ value: d.value, variation: d.variation, reason: d.reason }, c.name).toStrictEqual({
          value: c.value,
          variation: c.variation,
          reason: c.client_reason,
        });
      }
    } finally {
      await client.close();
    }
  });

  test("a closing page's beacon delivers its events", { timeout: 60_000 }, async () => {
    const before = await aliasesCounted();
    await commands.closePage(seed.replicas.a, seed.client_key, seed.unit_type);
    // The volume counts what the rollup read, every ten seconds.
    await eventually(async () => (await aliasesCounted()) > before, 30_000, "the alias the closing page handed to a beacon was never counted");
  });

  test("the SDK is in the inventory under its own name", { timeout: 100_000 }, async () => {
    // Each replica writes the SDKs it saw once a minute, so the entry of
    // the requests above appears within 90 seconds.
    await eventually(
      async () => {
        const inventory = JSON.parse(await commands.e2eHarness("inventory")) as InventoryEntry[];
        return inventory.some((e) => e.sdk_name === "flagnetic-js" && e.sdk_version === VERSION && e.platform === "browser");
      },
      90_000,
      `flagnetic-js ${VERSION} in a browser never appeared in the inventory`,
    );
  });

  test("a revocation through replica A refuses the key on replica B", { timeout: 30_000 }, async () => {
    await commands.e2eHarness("revoke", "client");
    await eventually(
      async () => {
        const client = createClient(seed.client_key, seed.replicas.b, { context: seed.cases[0]?.context ?? {}, store: false });
        try {
          return (await client.ready(2_000)) === "built_in";
        } finally {
          await client.close();
        }
      },
      20_000,
      "replica B kept answering the revoked client key",
    );
  });
});
