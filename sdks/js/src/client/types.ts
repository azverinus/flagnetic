import type { Context } from "../evaluator/evaluate.js";
import type { JsonValue } from "../evaluator/ruleset.js";
import { fitsFlagType, flagTypeOf, type Experiment, type FlagType, type Stage } from "../internal/detail.js";
import type { LogLevel } from "../internal/logger.js";

export type { Context, Experiment, FlagType, JsonValue, LogLevel, Stage };
export { fitsFlagType, flagTypeOf };

/**
 * The evaluation reason of a served value. A client key learns
 * `targeting_match` wherever targeting decided the value, never which rule,
 * rollout or prerequisite did; the server produces it and `experiment`,
 * `override` and `off`, and the SDK produces `local_override` and
 * `fallback`. A reason this SDK does not know is passed through as it
 * arrived, so a reason added to the server never turns into a failure.
 */
export type Reason =
  | "targeting_match"
  | "experiment"
  | "override"
  | "off"
  | "local_override"
  | "fallback"
  | (string & {});

/**
 * Why a detail has reason `fallback`. The server's evaluator error codes
 * (`inconsistent_ruleset`, `prerequisite_cycle`, …) are passed through as
 * they arrived; the rest the SDK decides.
 */
export type ErrorCode =
  /** The held values hold no flag with the key. */
  | "flag_not_found"
  /** The flag's type differs from the getter's. */
  | "type_mismatch"
  /** Stage `built_in`: nothing to serve from. */
  | "no_values"
  /** The served value does not fit the flag's type. */
  | "inconsistent_ruleset"
  /** An internal failure was caught at the API boundary. */
  | "general"
  | (string & {});

/** A served value and why it was served. */
export interface Detail<T = JsonValue> {
  /** The served value, or the built-in default as passed with reason `fallback`. */
  readonly value: T;
  readonly reason: Reason;
  /** The served variation's key; absent for `fallback` and `local_override`. */
  readonly variation?: string;
  /** Set with reason `fallback`. */
  readonly errorCode?: ErrorCode;
  /** The ruleset version the held values were evaluated against; absent at stage `built_in`. */
  readonly rulesetVersion?: number;
  /** When the held values were received: the age of last good values, which never expire. */
  readonly receivedAt?: Date;
  readonly stage: Stage;
  /**
   * True when a server override decided the value, directly or through a
   * prerequisite. A local override is reason `local_override` instead.
   */
  readonly overridden: boolean;
  /** Present when the value came from a running experiment, an override landing in one included. */
  readonly experiment?: Experiment;
}

/** Where the fallback chain stands. */
export interface Status {
  readonly stage: Stage;
  /** Since when the most recent attempts have been failing; absent after a success. */
  readonly staleSince?: Date;
  /** The ruleset version the held values were evaluated against; absent at stage `built_in`. */
  readonly rulesetVersion?: number;
  /** When the held values were received. */
  readonly receivedAt?: Date;
}

/**
 * Stable log event names: alert on them.
 *
 * - `values_loaded` (info): values were accepted. Fields: source (`network`,
 *   `store`, `bootstrap`, `offline`), ruleset_version, flags.
 * - `fetch_failed` (warn): a remote evaluation failed; the held values keep
 *   serving. Fields: cause, status?, code?, retry_in_ms.
 * - `stream_connected` (info) and `stream_dropped` (warn): the ruleset stream.
 *   Fields of the drop: cause, retry_in_ms.
 * - `stage_changed` (warn when leaving `fresh`, else info). Fields: from, to.
 * - `key_revoked` (error): 401 or 403, logged once until a fetch succeeds. Fields: status.
 * - `schema_unsupported` (error): a document's schema_version is not one this
 *   SDK reads, logged once. Fields: schema_version, supported.
 * - `attribute_ignored` (warn): the server ignored a context attribute,
 *   logged once per name. Fields: name, code.
 * - `queue_dropped` (warn): events or summary rows were given up on; `cause`
 *   names why: `queue_full` (the queue dropped its oldest events; fields
 *   dropped, capacity), `window_too_old` (a summary window began over an hour
 *   ago; fields kind, rows, age_ms) or `refused_by_the_platform` (a 202
 *   rejected events, which are not sent again; fields events, codes).
 * - `events_failed` (warn): an ingest batch failed. Fields: cause, status?,
 *   retry_in_ms, events.
 * - `value_too_deep` (error): a flag of an accepted document holds a value
 *   nesting arrays and objects deeper than the server stores (`max_depth`,
 *   32) and serves the fallback with `inconsistent_ruleset`, logged once per
 *   flag and ruleset version. Fields: flag, max_depth, ruleset_version.
 * - `store_failed` (warn): browser storage could not be read or written; the
 *   client carries on in memory. Fields: operation, key, cause.
 * - `override_set`, `override_refused`, `local_overrides_allowed` (warn):
 *   local overrides. Fields: flag, cause.
 * - `exposures_off` (warn): `sendExposures` is off, so no exposure leaves
 *   this browser.
 * - `type_mismatch` (warn): a getter asked for another type than the flag's,
 *   logged once per flag. Fields: flag, flag_type, requested_type.
 * - `evaluation_failed`, `listener_failed` (error): an internal failure or a
 *   throwing listener, caught. Fields: cause.
 */
export const LOG_EVENTS = [
  "values_loaded",
  "fetch_failed",
  "stream_connected",
  "stream_dropped",
  "stage_changed",
  "key_revoked",
  "schema_unsupported",
  "attribute_ignored",
  "value_too_deep",
  "queue_dropped",
  "events_failed",
  "store_failed",
  "override_set",
  "override_refused",
  "local_overrides_allowed",
  "exposures_off",
  /** An exposure call that sent nothing, naming why, at debug level. */
  "exposure_skipped",
  "type_mismatch",
  "evaluation_failed",
  "listener_failed",
  /** An operation of the SDK API answered with a `Deprecation` header, once per operation. Fields: operation, deprecation, sunset (when set). */
  "operation_deprecated",
] as const;

/** One of {@link LOG_EVENTS}. */
export type LogEvent = (typeof LOG_EVENTS)[number];

/**
 * Receives every log event with its level and structured fields. Called
 * synchronously; a throwing logger is ignored.
 */
export type Logger = (level: LogLevel, event: LogEvent, fields: Readonly<Record<string, unknown>>) => void;

/**
 * Where a client keeps what must survive a reload: the last good values of
 * each recent unit set and the event queue. `localStorage` and
 * `sessionStorage` both fit as they are.
 *
 * Every call may throw — a browser with storage disabled throws on the first
 * read: the client logs `store_failed` once and carries on in memory, so a
 * broken store never breaks a flag check.
 */
export interface ClientStore {
  /** The value under key, or null. */
  getItem(key: string): string | null;
  /** Stores value under key. */
  setItem(key: string, value: string): void;
  /** Removes key; a missing key is not an error. */
  removeItem(key: string): void;
}

/**
 * A values document as remote evaluation serves it: JSON text or an already
 * parsed value. A document saved from the server, or one the Node SDK
 * produced during server rendering, works as it is.
 */
export type ValuesSource = string | object;

export interface ClientOptions {
  /**
   * The context evaluated for: unit IDs by unit type plus attributes.
   * `setContext` replaces it. Empty by default: only attribute rules apply
   * and rollouts, experiments and overrides are skipped.
   */
  readonly context?: Context;
  /**
   * Values to start from, served as `fresh`: the document `values(context)`
   * of `flagnetic/server` produced while rendering the page, so hydration
   * matches. The client still fetches.
   */
  readonly bootstrap?: ValuesSource;
  /**
   * Offline mode: a values document, or a function returning one that is
   * called at construction and on every reload. No network call is ever
   * made; the stage is `fresh`; events go to the in-memory recorder.
   */
  readonly offline?: ValuesSource | (() => ValuesSource);
  /**
   * Where last good values and the event queue persist, under the SDK key:
   * `localStorage` by default, or nothing at all with `false`. A store that
   * throws is replaced by an in-memory one for the rest of the session.
   */
  readonly store?: ClientStore | false;
  /**
   * Whether to hold the ruleset stream, which brings a change within seconds
   * at the cost of one connection per tab: false by default.
   */
  readonly stream?: boolean;
  /** Milliseconds between refetches while the tab is visible: 300 000 by default, 30 000 at least. */
  readonly pollInterval?: number;
  /** Milliseconds a remote evaluation or an events request may take: 5 000 by default. */
  readonly requestTimeout?: number;
  /**
   * Milliseconds between event flushes while the tab is visible, of a queue
   * that holds an exposure or an alias: 5 000 by default. A queue holding
   * only an evaluation summary waits for the summary interval.
   */
  readonly flushInterval?: number;
  /**
   * Milliseconds between evaluation summaries while the tab is visible: 60 000
   * by default. The summary is also reported when the tab is hidden and when
   * the client closes. Exposures and aliases keep the flush interval.
   */
  readonly summaryInterval?: number;
  /** Whether exposures are sent: true by default. Off logs a warning at construction. */
  readonly sendExposures?: boolean;
  /** Whether evaluation summaries are sent: true by default. */
  readonly sendSummaries?: boolean;
  /** Enables local overrides; the client logs `local_overrides_allowed` at construction. */
  readonly allowOverrides?: boolean;
  /** Replaces the default logger, `consoleLogger("warn")`. */
  readonly logger?: Logger;
  /**
   * How this build reports itself: the context's `platform` attribute unless
   * the context sets one, the events envelope and every exposure. `browser`
   * by default.
   */
  readonly platform?: string;
  /** The `app_version` attribute unless the context sets one: the application's semantic version. */
  readonly appVersion?: string;
}

/** A deep copy of a decoded JSON value, so a caller may keep and mutate what a getter served. */
export function copyJson<T extends JsonValue>(value: T): T {
  if (Array.isArray(value)) return (value as readonly JsonValue[]).map(copyJson) as unknown as T;
  if (typeof value === "object" && value !== null) {
    const out: Record<string, JsonValue> = {};
    // defineProperty, so a "__proto__" key stays data.
    for (const [k, v] of Object.entries(value)) Object.defineProperty(out, k, { value: copyJson(v), enumerable: true, writable: true, configurable: true });
    return out as T;
  }
  return value;
}

/**
 * Deep equality of decoded JSON values. It walks both values with a stack of
 * its own rather than recursing, so a value nested however deeply compares
 * without overflowing the call stack.
 */
export function jsonEquals(a: JsonValue | undefined, b: JsonValue | undefined): boolean {
  const pending: [JsonValue | undefined, JsonValue | undefined][] = [[a, b]];
  for (let pair = pending.pop(); pair !== undefined; pair = pending.pop()) {
    const [x, y] = pair;
    if (x === y) continue;
    if (typeof x !== "object" || x === null || typeof y !== "object" || y === null) return false;
    const listX = asList(x);
    const listY = asList(y);
    if (listX || listY) {
      if (!listX || !listY || listX.length !== listY.length) return false;
      listX.forEach((v, i) => pending.push([v, listY[i]]));
      continue;
    }
    const ox = x as Record<string, JsonValue>;
    const oy = y as Record<string, JsonValue>;
    const keys = Object.keys(ox);
    if (keys.length !== Object.keys(oy).length) return false;
    for (const k of keys) {
      if (!Object.hasOwn(oy, k)) return false;
      pending.push([ox[k], oy[k]]);
    }
  }
  return true;
}

function asList(v: JsonValue): readonly JsonValue[] | undefined {
  return Array.isArray(v) ? (v as readonly JsonValue[]) : undefined;
}
