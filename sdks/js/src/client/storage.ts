// Browser storage: the last good values of each recent unit set and the
// event queue. Everything here is synchronous, so values serve from the
// moment the constructor returns; a store that throws is replaced by an
// in-memory one for the rest of the session: a throwing store yields last
// good values or built-in defaults, never a crash.
import { causeOf, type LogLevel } from "../internal/logger.js";
import type { ClientStore, LogEvent } from "./types.js";

/** The prefix of every key a client writes. */
export const STORE_PREFIX = "flagnetic.";

/**
 * The keys one SDK key writes. Two clients sharing a browser's storage — a
 * staging key beside a production one, or two installations — must never
 * serve each other's values or send each other's events under their own
 * key, so the SDK key, which is what names an environment of an
 * installation, names every key and the unit set names only the values.
 * The base URL takes no part: the same key behind a proxy, a
 * renamed host or another port is the same environment.
 */
export class StoreKeys {
  /** The key listing this connection's stored unit sets, most recently fetched first. */
  readonly index: string;
  /** The key of this connection's persisted event queue. */
  readonly events: string;

  constructor(sdkKey: string) {
    const scope = digest(sdkKey);
    this.index = `${STORE_PREFIX}${scope}.last_good`;
    this.events = `${STORE_PREFIX}${scope}.events`;
  }

  /**
   * The key of the last good values of a unit set: a hash of its unit types
   * and IDs, sorted, so the order a context lists them in does not matter.
   * The entry repeats the units and a hit is checked against them.
   */
  lastGood(units: Readonly<Record<string, string>>): string {
    const canonical = JSON.stringify(
      Object.keys(units)
        .sort()
        .map((type) => [type, units[type]]),
    );
    return `${this.index}.${digest(canonical)}`;
  }
}

/** True when a and b hold the same unit IDs by unit type. */
export function sameUnits(a: Readonly<Record<string, string>>, b: Readonly<Record<string, string>>): boolean {
  const ka = Object.keys(a);
  return ka.length === Object.keys(b).length && ka.every((k) => b[k] === a[k]);
}

/** A {@link ClientStore} in memory: nothing survives the tab. */
export class MemoryStore implements ClientStore {
  private readonly entries = new Map<string, string>();

  getItem(key: string): string | null {
    return this.entries.get(key) ?? null;
  }

  setItem(key: string, value: string): void {
    this.entries.set(key, value);
  }

  removeItem(key: string): void {
    this.entries.delete(key);
  }
}

/**
 * The browser's `localStorage`, or nothing when it cannot even be reached —
 * a data-URL document or a browser with site data blocked throws on the
 * property itself.
 */
export function defaultStore(): ClientStore | undefined {
  try {
    const storage = (globalThis as { localStorage?: ClientStore }).localStorage;
    return typeof storage?.getItem === "function" ? storage : undefined;
  } catch {
    return undefined;
  }
}

/**
 * A store that never throws: every call goes to the wrapped store until one
 * fails, and from then on to an in-memory one, so a quota error or a storage
 * disabled mid-session degrades to "this tab only" instead of breaking a
 * flag check. Each failure is logged as `store_failed`.
 */
export class SafeStore {
  private fallback: MemoryStore | undefined;

  constructor(
    private readonly store: ClientStore | undefined,
    private readonly log: (level: LogLevel, event: LogEvent, fields: Record<string, unknown>) => void,
  ) {
    if (!store) this.fallback = new MemoryStore();
  }

  /** Whether reads and writes still reach the browser's own storage. */
  get persistent(): boolean {
    return this.fallback === undefined;
  }

  get(key: string): string | null {
    return this.call("get", key, (s) => s.getItem(key), null);
  }

  set(key: string, value: string): void {
    this.call("set", key, (s) => { s.setItem(key, value); }, undefined);
  }

  remove(key: string): void {
    this.call("remove", key, (s) => { s.removeItem(key); }, undefined);
  }

  private call<T>(operation: string, key: string, fn: (s: ClientStore) => T, onFailure: T): T {
    const store = this.fallback ?? this.store;
    if (!store) return onFailure;
    try {
      return fn(store);
    } catch (e) {
      this.log("warn", "store_failed", { operation, key, cause: causeOf(e) });
      // The browser's storage is out; the rest of the session is in memory.
      if (this.fallback === undefined) this.fallback = new MemoryStore();
      try {
        return fn(this.fallback);
      } catch {
        return onFailure;
      }
    }
  }
}

// An eight-character hex digest of text, for a storage key's own use.
function digest(text: string): string {
  return hash32(text).toString(16).padStart(8, "0");
}

// A 32-bit FNV-1a hash of the UTF-8 bytes of text. It names a storage key and
// nothing else: no bucket, no assignment, nothing an analyst recomputes.
function hash32(text: string): number {
  const bytes = new TextEncoder().encode(text);
  let h = 0x811c9dc5;
  for (const b of bytes) {
    h ^= b;
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}
