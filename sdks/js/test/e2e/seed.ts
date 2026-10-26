// What the smokes read from the end-to-end harness (e2e/README.md): the
// seed `flagnetic-e2e up` writes and the answers of its control commands.
// Fields are only ever added to them, so each type names what a smoke uses.
import type { JsonValue } from "../../src/index.js";

export interface SeedCase {
  name: string;
  flag: string;
  context: { units?: Record<string, string>; attributes?: Record<string, JsonValue> };
  value: JsonValue;
  variation: string;
  reason: string;
  client_reason: string;
}

export interface Seed {
  replicas: { a: string; b: string };
  server_key: string;
  client_key: string;
  unit_type: string;
  flags: { on: string };
  cases: SeedCase[];
}

/** The answer of `flagnetic-e2e kill`. */
export interface Killed {
  committed_at: string;
}

/** One entry of `flagnetic-e2e inventory`. */
export interface InventoryEntry {
  sdk_name: string;
  sdk_version: string;
  platform: string | null;
}

/** The answer of `flagnetic-e2e events`. */
export interface EventVolume {
  days: { day: string; type: string; events: number }[];
}

/** Every alias the project counted, over the days the volume covers. */
export function aliases(volume: EventVolume): number {
  return volume.days.filter((d) => d.type === "alias").reduce((sum, d) => sum + d.events, 0);
}

/** A built-in default the flag never serves, so a fallback cannot pass for the case's value. */
export function unlike(value: JsonValue): JsonValue {
  switch (typeof value) {
    case "boolean":
      return !value;
    case "number":
      return value - 1;
    case "string":
      return `not ${value}`;
    default:
      return { not: value };
  }
}

/** Calls check every half second until it answers true, or fails after timeoutMs. */
export async function eventually(check: () => Promise<boolean>, timeoutMs: number, message: string): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (!(await check())) {
    if (Date.now() > deadline) throw new Error(message);
    await new Promise((r) => setTimeout(r, 500));
  }
}
