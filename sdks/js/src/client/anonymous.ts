// The anonymous ID: one unit ID per origin and unit type, minted once and
// kept in browser storage outside every SDK key's scope. Cached values and
// the event queue live under the key; the ID must not, since a rotated or
// replaced key would otherwise give every anonymous visitor a new ID and
// move them to another experiment arm.
import { uuidV7 } from "../internal/uuid.js";
import { defaultStore, STORE_PREFIX } from "./storage.js";
import type { ClientStore } from "./types.js";

/** The IDs minted in this page, for when storage cannot keep them. */
const minted = new Map<string, string>();

/**
 * The unit ID this browser uses for the anonymous unit type unitType,
 * minting a random UUIDv7 the first time and keeping it in `localStorage`
 * for every later visit of this origin, whatever SDK key the page uses.
 *
 * The unit type is the application's: pass the one the project declared for
 * anonymous traffic, and keep the ID in the context's units after sign-in so
 * an experiment spanning sign-up does not change variation mid funnel.
 *
 * ```ts
 * const flagnetic = createClient("fn_cli_…", "https://flags.example.com", {
 *   context: { units: { anonymous: anonymousId() } },
 * });
 * ```
 *
 * Total: where `localStorage` cannot be reached or throws, the ID holds for
 * this page only.
 */
export function anonymousId(unitType = "anonymous"): string {
  return anonymousIdIn(defaultStore(), unitType);
}

/** anonymousId over a given store; not part of the public entry point. */
export function anonymousIdIn(store: ClientStore | undefined, unitType: string): string {
  const key = `${STORE_PREFIX}anonymous.${unitType}`;
  let id: string | null | undefined;
  try {
    id = store?.getItem(key);
  } catch {
    // Storage blocked: fall back to this page's own.
  }
  if (id) return id;
  id = minted.get(key) ?? uuidV7(Date.now(), random);
  minted.set(key, id);
  try {
    store?.setItem(key, id);
  } catch {
    // Storage full or blocked: the ID holds for this page.
  }
  return id;
}

// The ID is what an anonymous visitor is bucketed by, so it takes its random
// bits from the platform's cryptographic source where there is one.
function random(): number {
  const crypto = (globalThis as { crypto?: { getRandomValues?: (a: Uint32Array) => Uint32Array } }).crypto;
  return crypto?.getRandomValues ? (crypto.getRandomValues(new Uint32Array(1))[0] ?? 0) / 2 ** 32 : Math.random();
}
