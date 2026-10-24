import type { ErrorCode, Reason } from "../evaluator/evaluate.js";
import type { JsonValue } from "../evaluator/ruleset.js";
import type { Experiment, FlagType, Stage } from "../internal/detail.js";
import type { LogLevel } from "../internal/logger.js";

export type { Experiment, FlagType, Stage };

/**
 * The evaluation reason of a served value. The evaluator produces the
 * first eight; the SDK produces `local_override` and `fallback`. A getter
 * never serves `error`: an evaluator error serves the built-in default with
 * reason `fallback`.
 */
export type DetailReason = Exclude<Reason, "error"> | "local_override" | "fallback";

/**
 * Why a detail has reason `fallback`: the evaluator's error codes, plus
 * `type_mismatch` (the flag's type differs from the getter's), `no_ruleset`
 * (stage `built_in`) and `general` (an internal failure caught at the API
 * boundary).
 */
export type DetailErrorCode = ErrorCode | "type_mismatch" | "no_ruleset" | "general";

/** A served value and why it was served. */
export interface Detail<T = JsonValue> {
  /** The served value, or the built-in default as passed with reason `fallback`. */
  readonly value: T;
  readonly reason: DetailReason;
  /** The served variation's key; absent for `fallback` and `local_override`. */
  readonly variation?: string;
  /** The rule that served, for reasons `rule`, `rollout` and `experiment`. */
  readonly ruleId?: string;
  /** Set with reason `fallback`. */
  readonly errorCode?: DetailErrorCode;
  /** The version of the ruleset evaluated against; absent at stage `built_in`. */
  readonly rulesetVersion?: number;
  /** When that ruleset was received: the age of a last good ruleset. */
  readonly receivedAt?: Date;
  readonly stage: Stage;
  /**
   * True when a platform override decided the value, directly or through a
   * prerequisite. A local override is reason `local_override` instead.
   */
  readonly overridden: boolean;
  /** Present when the value came from a running experiment, an override landing in one included. */
  readonly experiment?: Experiment;
}

export type { LogLevel };

/**
 * Stable log event names: alert on them.
 *
 * - `ruleset_loaded` (info): a ruleset was accepted. Fields: source, ruleset_version, flags.
 * - `fetch_failed` (warn): a ruleset fetch failed. Fields: cause, status?, retry_in_ms (when the next attempt is due).
 * - `sse_connected` (info) and `sse_dropped` (warn): the change stream. Fields of the drop: cause, retry_in_ms.
 * - `stage_changed` (warn when leaving `fresh`, else info). Fields: from, to.
 * - `key_revoked` (error): 401 or 403, logged once until a fetch succeeds. Fields: status.
 * - `schema_unsupported` (error): a document's schema_version is not one this SDK reads, logged once. Fields: schema_version, supported.
 * - `rule_unsupported` (warn): a flag reached a construct the evaluator cannot apply, logged once per flag and ruleset version. Fields: flag, error_code.
 * - `store_failed` (warn): the store could not be read or written. Fields: operation, cause.
 * - `events_failed` (warn): an ingest batch failed and goes again with the same event IDs. Fields: cause, status?, events, retry_in_ms.
 * - `queue_dropped` (warn): events or summary rows were given up on. Fields: cause, plus what was dropped.
 * - `exposures_off` (warn): `sendExposures` is off, so no exposure or alias leaves this client. Logged once, at construction.
 * - `exposure_skipped` (debug): the exposure call found no experiment block. Fields: flag, cause.
 * - `override_set`, `override_refused`, `local_overrides_allowed` (warn): local overrides. Fields: flag, cause.
 * - `type_mismatch` (warn): a getter asked for another type than the flag's, logged once per flag. Fields: flag, flag_type, requested_type.
 * - `evaluation_failed`, `listener_failed` (error): an internal failure or a throwing listener, caught. Fields: cause.
 */
export type LogEvent =
  | "ruleset_loaded"
  | "fetch_failed"
  | "sse_connected"
  | "sse_dropped"
  | "stage_changed"
  | "key_revoked"
  | "schema_unsupported"
  | "rule_unsupported"
  | "store_failed"
  | "events_failed"
  | "queue_dropped"
  | "exposures_off"
  | "exposure_skipped"
  | "override_set"
  | "override_refused"
  | "local_overrides_allowed"
  | "type_mismatch"
  | "evaluation_failed"
  | "listener_failed";

/**
 * Receives every log event with its level and structured fields. Called
 * synchronously; a throwing logger is ignored.
 */
export type Logger = (level: LogLevel, event: LogEvent, fields: Readonly<Record<string, unknown>>) => void;

/**
 * A ruleset document as the ruleset endpoint serves it: JSON text, UTF-8
 * bytes or an already parsed value.
 */
export type RulesetSource = string | Uint8Array | object;

export interface ServerOptions {
  /**
   * Offline mode: a ruleset document, or a function returning one that is
   * called at construction and on every reload. No network call is ever
   * made; the stage is `fresh`; evaluations are counted in the recorder.
   */
  readonly offline?: RulesetSource | (() => RulesetSource);
  /** Enables local overrides; the client logs `local_overrides_allowed` at construction. */
  readonly allowOverrides?: boolean;
  /** Replaces the default logger, `consoleLogger("warn")`. */
  readonly logger?: Logger;
  /** Milliseconds between safety polls while the change stream is connected: 300 000 by default, 30 000 at least. */
  readonly pollInterval?: number;
  /** Milliseconds between polls while the stream is down or disabled: 30 000 by default and at least. */
  readonly pollIntervalWithoutStream?: number;
  /** Whether to open the change stream: true by default. Without it the client polls at `pollIntervalWithoutStream`. */
  readonly stream?: boolean;
  /** Milliseconds a ruleset request may take: 10 000 by default. */
  readonly requestTimeout?: number;
  /**
   * A file path holding the last good ruleset: loaded as `last_good` before
   * any network call, rewritten on every accepted ruleset.
   */
  readonly store?: string;
  /** Reported in the User-Agent, in the events envelope and on every exposure: `node` by default. */
  readonly platform?: string;
  /**
   * The application's version, reported with the evaluation summary and on
   * every exposure a context does not name an `app_version` attribute in.
   */
  readonly appVersion?: string;
  /** Whether evaluation summaries are reported: true by default. */
  readonly sendSummaries?: boolean;
  /** Milliseconds between flushes of the exposure and alias queue: 2 000 by default. */
  readonly flushInterval?: number;
  /** Milliseconds between evaluation summaries: 60 000 by default. */
  readonly summaryInterval?: number;
  /**
   * Whether exposures and aliases are sent: true by default. Off logs
   * `exposures_off` at construction.
   */
  readonly sendExposures?: boolean;
}
