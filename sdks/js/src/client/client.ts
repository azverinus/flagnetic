// The browser client: it posts its context to /sdk/v1/evaluate, serves
// every flag's value from memory, keeps the last good values in browser
// storage under the SDK key and the context's unit set, and refetches when
// the tab becomes visible, the network returns, the poll interval passes or
// the opt-in stream announces a change. It never evaluates locally and
// never imports the evaluator, which keeps it small in a bundle.
import { Backoff } from "../internal/backoff.js";
import { realClock, type Clock } from "../internal/clock.js";
import { trimTrailingSlashes } from "../internal/http.js";
import { causeOf, consoleLogger, safeLog, type LogLevel } from "../internal/logger.js";
import { SDK_API_PATH, VERSION } from "../version.js";
import { aliasEvent, exposureEvent, ExposureSuppressor, type AliasLink } from "../internal/events.js";
import { Ingest, SummaryCounter, type QueuedEvent } from "./ingest.js";
import { LocalOverrideRefused, LocalOverridesNotAllowed } from "../internal/overrides.js";
import { defaultBeacon, defaultPage, type Page } from "./page.js";
import { Recorder } from "./recorder.js";
import { serveDetail, type ServeBase } from "./serve.js";
import { defaultStore, SafeStore, sameUnits, StoreKeys } from "./storage.js";
import { ChangeStream, defaultStreamTiming, type StreamTiming } from "./stream.js";
import {
  copyJson,
  fitsFlagType,
  flagTypeOf,
  type ClientOptions,
  type Context,
  type Detail,
  type ErrorCode,
  type FlagType,
  type JsonValue,
  type LogEvent,
  type Logger,
  type Reason,
  type Stage,
  type Status,
  type ValuesSource,
} from "./types.js";
import { Refused, SCHEMA_VERSION, SUPPORTED_SCHEMA_VERSIONS, ValuesDocument } from "./values.js";

/** This SDK's name, in the events envelope and the `sdk` context attribute. */
export const SDK_NAME = "flagnetic-js";

/** The lowest poll interval the options accept, in milliseconds, so that no option can hammer the server. */
const pollFloor = 30_000;

/** The browser client's timing, in milliseconds. */
const defaults = {
  pollInterval: 300_000,
  minRefetchGap: 10_000,
  requestTimeout: 5_000,
  flushInterval: 5_000,
  retryInitial: 2_000,
  retryMax: 300_000,
  unauthorizedMax: 900_000,
  closeTimeout: 5_000,
  batchSize: 100,
  capacity: 1_000,
  storedUnitSets: 4,
  /** How long the first evaluation waits for the change stream to connect. */
  streamFirstWait: 2_000,
  /** The most (experiment, unit, variation) triples the in-process exposure suppressor holds. */
  exposureMemory: 1_000,
};

export type Timing = typeof defaults;

/** What the client handled, for a test driving a virtual clock. */
export type ClientTraceEvent = "fetch_handled" | "events_handled";

/** Test seams: timing, timers, randomness, the network and the page. */
export interface ClientInternals {
  readonly timing?: Partial<Timing>;
  readonly streamTiming?: Partial<StreamTiming>;
  readonly clock?: Clock;
  readonly random?: () => number;
  /** The wall clock behind timestamps and event IDs. */
  readonly now?: () => number;
  readonly fetch?: typeof fetch;
  readonly page?: Page;
  /** Stands in for `navigator.sendBeacon`; null for a page without one. */
  readonly beacon?: ((url: string, body: string) => boolean) | null;
  readonly trace?: (event: ClientTraceEvent) => void;
  /**
   * Makes no request until the first `setContext`, for an owner that learns
   * the context only after building the client: the OpenFeature web
   * provider. An evaluation of the constructor's context would be thrown
   * away.
   */
  readonly deferStart?: boolean;
}

/** Why an evaluation was started; it reaches the `fetch_failed` log event. */
type Trigger = "start" | "context" | "visible" | "online" | "interval" | "stream" | "retry";

/**
 * Constructs a browser client for a client key and the server's base URL.
 * It fails loudly only for a server key (`fn_srv_…`), which must never ship
 * in a bundle; network trouble never makes it throw.
 *
 * Values serve synchronously from the moment this returns: `bootstrap` as
 * `fresh`, else the last good values browser storage holds for the context's
 * unit set as `last_good`, else built-in defaults.
 */
export function createClient(sdkKey: string, baseUrl: string, options: ClientOptions = {}): FlagneticClient {
  return new FlagneticClient(sdkKey, baseUrl, options, {});
}

/** createClient with test seams; not part of the public entry point. */
export function createClientWith(sdkKey: string, baseUrl: string, options: ClientOptions, internals: ClientInternals): FlagneticClient {
  return new FlagneticClient(sdkKey, baseUrl, options, internals);
}

/**
 * A client-mode client. Every public member is total: an internal failure is
 * caught, logged and never escapes, and a getter with nothing to serve
 * returns its built-in default with reason `fallback`.
 */
export class FlagneticClient {
  /** Local overrides; they apply only with the `allowOverrides` option. */
  readonly overrides: LocalOverrides;
  /** The test helpers of an offline client; undefined online. */
  readonly offline: OfflineMode | undefined;

  private context: Context;
  private values: ValuesDocument | undefined;
  private receivedAt: Date | undefined;
  private currentStage: Stage = "built_in";
  private staleSince: Date | undefined;

  private readonly sdkKey: string;
  private readonly baseUrl: string;
  private readonly options: ClientOptions;
  private readonly timing: Timing;
  private readonly logger: Logger;
  private readonly store: SafeStore;
  /** The keys this connection writes: its own values and its own event queue. */
  private readonly keys: StoreKeys;
  private readonly clock: Clock;
  private readonly now: () => number;
  private readonly random: () => number;
  private readonly doFetch: typeof fetch;
  private readonly page: Page;
  private readonly traceEvent: ((event: ClientTraceEvent) => void) | undefined;
  private readonly summary: SummaryCounter<Reason>;
  /**
   * The most recent detail of every flag whose evaluation carried an
   * experiment block, which is what an exposure call reports. A flag that
   * stops carrying one is forgotten, so the set is bounded by the running
   * experiments of the values held, and the call sends nothing rather than
   * the assignment of an experiment the server no longer serves.
   */
  private readonly served = new Map<string, Detail>();
  private readonly ingest: Ingest | undefined;
  private readonly recorder: Recorder | undefined;
  private stream: ChangeStream | undefined;
  private readonly streamTiming: Partial<StreamTiming> | undefined;
  private readonly unlisten: () => void;

  // The evaluation loop. Every trigger coalesces into the request in flight.
  private inFlight = false;
  private again = false;
  private started = false;
  /** No request until the first setContext: see ClientInternals.deferStart. */
  private deferred: boolean;
  private retrying = false;
  private lastAttemptAt = Number.NEGATIVE_INFINITY;
  /** The poll or the retry, whichever is due: the next attempt is one timer. */
  private nextAttempt: unknown;
  private firstWait: unknown;
  /** Aborts the evaluation in flight; close calls it so a late answer or error touches nothing. */
  private abortInFlight: (() => void) | undefined;
  private attemptSeq = 0;
  private readonly attemptWaiters: { readonly minSeq: number; readonly resolve: (stage: Stage) => void }[] = [];
  private readonly backoff: Backoff;
  private unauthorized = false;
  private announced = 0;
  /**
   * The context whose values were last accepted. Values of another context
   * set no floor for the version: a replica a little behind may answer the
   * new context with a lower ruleset version, and that answer is still the
   * only one there is for it.
   */
  private acceptedContext: Context | undefined;

  private readyWaiters: (() => void)[] = [];
  private isReady = false;
  private closed = false;
  private visible: boolean;
  private readonly logged = new Set<string>();
  private readonly exposed: ExposureSuppressor;
  private readonly changeListeners = new Set<(keys: string[]) => void>();
  private readonly stageListeners = new Set<(stage: Stage) => void>();
  /** @internal */
  readonly allowOverrides: boolean;
  /** @internal */
  localOverrides = new Map<string, { readonly type: FlagType; readonly value: JsonValue }>();

  /** @internal Use createClient. */
  constructor(sdkKey: string, baseUrl: string, options: ClientOptions, internals: ClientInternals) {
    this.sdkKey = sdkKey;
    this.options = options;
    this.deferred = internals.deferStart === true;
    this.streamTiming = internals.streamTiming;
    this.logger = options.logger ?? consoleLogger("warn");
    this.allowOverrides = options.allowOverrides === true;
    this.overrides = new LocalOverrides(this);
    this.context = options.context ?? {};
    this.clock = internals.clock ?? realClock;
    this.now = internals.now ?? (() => Date.now());
    this.random = internals.random ?? Math.random;
    this.doFetch = internals.fetch ?? ((input, init) => fetch(input, init));
    this.page = internals.page ?? defaultPage();
    this.traceEvent = internals.trace;
    this.baseUrl = trimTrailingSlashes(baseUrl);
    this.timing = {
      ...defaults,
      ...(options.pollInterval !== undefined && { pollInterval: Math.max(pollFloor, options.pollInterval) }),
      ...(options.requestTimeout !== undefined && { requestTimeout: options.requestTimeout }),
      ...(options.flushInterval !== undefined && { flushInterval: options.flushInterval }),
      ...internals.timing,
    };
    this.backoff = new Backoff(this.timing.retryInitial, this.random);
    this.summary = new SummaryCounter<Reason>(this.now);
    this.exposed = new ExposureSuppressor(this.timing.exposureMemory);
    this.store = new SafeStore(options.store === false ? undefined : (options.store ?? defaultStore()), (l, e, f) => { this.log(l, e, f); });
    this.keys = new StoreKeys(sdkKey);
    this.visible = safeVisible(this.page);

    if (this.allowOverrides) this.log("warn", "local_overrides_allowed", {});
    if (options.sendExposures === false) this.log("warn", "exposures_off", {});

    // The one mistake this client refuses loudly, offline mode included: a
    // server key in a browser build is a leak whether or not it is used.
    if (typeof sdkKey === "string" && sdkKey.startsWith("fn_srv_")) {
      throw new Error("flagnetic/client needs a client key (fn_cli_…); a server key belongs to flagnetic/server and must never ship in a bundle");
    }
    if (options.offline !== undefined) {
      this.recorder = new Recorder();
      this.offline = new OfflineMode(this, options.offline);
      this.unlisten = () => undefined;
      this.offline.reload();
      this.markReady();
      return;
    }

    this.ingest = new Ingest(
      {
        baseUrl: this.baseUrl,
        sdkKey,
        sdkName: SDK_NAME,
        sdkVersion: VERSION,
        platform: this.platform(),
        ...(options.appVersion !== undefined && { appVersion: options.appVersion }),
        capacity: this.timing.capacity,
        batchSize: this.timing.batchSize,
        flushInterval: this.timing.flushInterval,
        requestTimeout: this.timing.requestTimeout,
        retryInitial: this.timing.retryInitial,
        retryMax: this.timing.retryMax,
        unauthorizedMax: this.timing.unauthorizedMax,
        sendSummaries: options.sendSummaries !== false,
        clock: this.clock,
        now: this.now,
        random: this.random,
        fetch: this.doFetch,
        beacon: internals.beacon === null ? undefined : (internals.beacon ?? defaultBeacon()),
        queueKey: this.keys.events,
        rulesetVersion: () => this.values?.rulesetVersion,
      },
      this.summary,
      this.store,
      (l, e, f) => { this.log(l, e, f); },
      () => { this.traceEvent?.("events_handled"); },
    );

    // Values serve from here on: bootstrap wins, else the store.
    if (options.bootstrap !== undefined) this.accept(options.bootstrap, "bootstrap");
    if (!this.values) this.loadStored();
    // Bootstrap and stored values answer the context the client starts with.
    if (this.values) this.acceptedContext = this.context;

    this.unlisten = this.page.listen({
      visible: () => { this.onVisible(); },
      hidden: () => { this.onHidden(); },
      online: () => { this.refetch("online"); },
    });
    this.ingest.setVisible(this.visible);
    this.ingest.start();

    if (!this.deferred) this.begin();
  }

  // begin opens the change stream when it is on and starts the first
  // evaluation, which with the stream waits for it to open.
  private begin(): void {
    if (this.options.stream === true) {
      this.stream = new ChangeStream(
        {
          url: `${this.baseUrl}${SDK_API_PATH}/stream`,
          sdkKey: this.sdkKey,
          clock: this.clock,
          random: this.random,
          fetch: this.doFetch,
          timing: { ...defaultStreamTiming, ...this.streamTiming },
        },
        {
          opened: () => {
            // Versions may have been missed while the stream was down.
            this.refetch("stream");
          },
          refused: (status) => {
            this.once("revoked", "error", "key_revoked", { status });
            // The first values must not wait out the grace period below for
            // a stream that will not open.
            if (!this.started) this.refetch("start");
          },
          announced: (version) => { this.onAnnounced(version); },
          heldVersion: () => this.values?.rulesetVersion,
          log: (l, e, f) => { this.log(l, e, f); },
        },
      );
      this.stream.open();
      // The stream opens first so that no change falls between the two; the
      // first evaluation waits for it no longer than this.
      this.firstWait = this.clock.setTimeout(() => { if (!this.started) this.refetch("start"); }, this.timing.streamFirstWait);
      return;
    }
    this.refetch("start");
  }

  // ------------------------------------------------------------ lifecycle

  /** The current stage of the fallback chain. */
  get stage(): Stage {
    return this.currentStage;
  }

  /** The stage, since when attempts have been failing, and the held values' version and age. */
  status(): Status {
    return {
      stage: this.currentStage,
      ...(this.staleSince && { staleSince: new Date(this.staleSince) }),
      ...(this.values && { rulesetVersion: this.values.rulesetVersion }),
      ...(this.receivedAt && { receivedAt: new Date(this.receivedAt) }),
    };
  }

  /**
   * Resolves with the stage reached once the first values arrive, at the
   * timeout in milliseconds, or at close. Never rejects. Evaluation before
   * ready is legal and follows the fallback chain.
   */
  ready(timeout?: number): Promise<Stage> {
    if (this.isReady || this.closed) return Promise.resolve(this.currentStage);
    return new Promise((resolve) => {
      let timer: unknown;
      const done = (): void => {
        this.clock.clearTimeout(timer);
        this.readyWaiters = this.readyWaiters.filter((w) => w !== done);
        resolve(this.currentStage);
      };
      this.readyWaiters.push(done);
      if (timeout !== undefined) timer = this.clock.setTimeout(done, Math.max(0, timeout));
    });
  }

  /**
   * Replaces the context and evaluates it. The previous values keep serving
   * until the response arrives, so signing in causes no flash of defaults;
   * the promise resolves with the stage the new values reached (`fresh`
   * once they arrived, `last_good` when the request failed) and the change
   * listeners receive the keys whose value differs. Never rejects.
   */
  setContext(context: Context): Promise<Stage> {
    if (this.closed || this.offline) return Promise.resolve(this.currentStage);
    this.context = context;
    // The details held answer the previous context; an exposure reports the
    // detail of the context the flag was evaluated for.
    this.served.clear();
    // The previous values are no longer the server's answer for this
    // context, so they serve as last good until the new ones arrive.
    if (this.currentStage === "fresh") this.setStage("last_good");
    if (this.deferred) {
      // The first context starts the client as the constructor would have:
      // with the stream on, the evaluation waits for it to open, so a page
      // load sends one evaluation and no change falls between the two.
      this.deferred = false;
      return this.attemptAfter(() => { this.begin(); });
    }
    return this.attemptAfter(() => { this.refetch("context"); });
  }

  /**
   * Registers listener, called with the sorted keys whose served value
   * changed: on every accepted document and on every local override change.
   * Returns a function that unregisters it.
   */
  onChange(listener: (keys: string[]) => void): () => void {
    this.changeListeners.add(listener);
    return () => this.changeListeners.delete(listener);
  }

  /** Registers listener, called on every stage transition. Returns a function that unregisters it. */
  onStage(listener: (stage: Stage) => void): () => void {
    this.stageListeners.add(listener);
    return () => this.stageListeners.delete(listener);
  }

  /**
   * Sends the queued exposures, aliases and the evaluation summary, waiting
   * at most timeout milliseconds (5 s by default); a batch not yet accepted
   * stays queued and goes again on its backoff. Never rejects.
   */
  async flush(timeout: number = this.timing.closeTimeout): Promise<void> {
    const flushed = (this.ingest?.flush() ?? Promise.resolve()).catch((e: unknown) => {
      this.log("error", "evaluation_failed", { operation: "flush", cause: causeOf(e) });
    });
    let timer: unknown;
    await Promise.race([flushed, new Promise<void>((resolve) => (timer = this.clock.setTimeout(resolve, Math.max(0, timeout))))]);
    this.clock.clearTimeout(timer);
  }

  /**
   * Stops the timers and the change stream and flushes best effort within
   * the close timeout. Values keep serving afterwards.
   */
  async close(): Promise<void> {
    if (this.closed) return;
    this.closed = true;
    this.clock.clearTimeout(this.nextAttempt);
    this.clock.clearTimeout(this.firstWait);
    this.abortInFlight?.();
    this.unlisten();
    this.stream?.close();
    const flushed = this.flush();
    this.markReady();
    this.resolve(Number.POSITIVE_INFINITY);
    await flushed;
    this.ingest?.close();
  }

  // -------------------------------------------------------------- getters

  /** Serves a boolean flag, or defaultValue with reason `fallback`. */
  getBool(key: string, defaultValue: boolean): boolean {
    return this.evaluate(key, "boolean", defaultValue).value;
  }

  /** Serves a string flag, or defaultValue with reason `fallback`. */
  getString(key: string, defaultValue: string): string {
    return this.evaluate(key, "string", defaultValue).value;
  }

  /** Serves a number flag, or defaultValue with reason `fallback`. */
  getNumber(key: string, defaultValue: number): number {
    return this.evaluate(key, "number", defaultValue).value;
  }

  /** Serves a JSON flag as a value the caller may keep and mutate, or defaultValue with reason `fallback`. */
  getJson(key: string, defaultValue: JsonValue): JsonValue {
    return this.evaluate(key, "json", defaultValue).value;
  }

  /**
   * Serves a flag with why it was served. The type of defaultValue names the
   * flag type asked for: boolean, string, number, or anything else as JSON.
   */
  getDetail(key: string, defaultValue: boolean): Detail<boolean>;
  getDetail(key: string, defaultValue: string): Detail<string>;
  getDetail(key: string, defaultValue: number): Detail<number>;
  getDetail(key: string, defaultValue: JsonValue): Detail;
  getDetail(key: string, defaultValue: JsonValue): Detail {
    return this.evaluate(key, flagTypeOf(defaultValue), defaultValue);
  }

  // The API boundary every getter goes through: it never throws, counts what
  // it served and emits the exposure an auto-exposure flag asks for.
  private evaluate<T extends JsonValue>(key: string, type: FlagType, def: T): Detail<T> {
    let d: Detail<T>;
    try {
      d = this.detail(key, type, def);
    } catch (e) {
      this.log("error", "evaluation_failed", { flag: key, cause: causeOf(e) });
      d = this.fallback(def, "general");
    }
    try {
      this.summary.count(key, d.variation, d.reason);
      // What an exposure call reports is what was served here, not what the
      // values document says by the time it is called.
      if (d.experiment) this.served.set(key, d);
      else this.served.delete(key);
      if (d.experiment && this.values?.flags.get(key)?.automaticExposure === true) this.emitExposure(key, d);
    } catch (e) {
      this.log("error", "evaluation_failed", { flag: key, cause: causeOf(e) });
    }
    return d;
  }

  private detail<T extends JsonValue>(key: string, type: FlagType, def: T): Detail<T> {
    return serveDetail(key, type, def, this.values, this.localOverride(key), this.base(), (flagType) => {
      this.once(`type:${key}`, "warn", "type_mismatch", { flag: key, flag_type: flagType, requested_type: type });
    });
  }

  private base(): ServeBase {
    return {
      stage: this.currentStage,
      overridden: false,
      ...(this.values && { rulesetVersion: this.values.rulesetVersion }),
      ...(this.receivedAt && { receivedAt: new Date(this.receivedAt) }),
    };
  }

  private fallback<T extends JsonValue>(def: T, code: ErrorCode): Detail<T> {
    return { ...this.base(), value: def, reason: "fallback", errorCode: code };
  }

  // --------------------------------------------------------------- events

  /**
   * Records that the unit actually encountered the behaviour the flag's
   * value decides. What is reported is the most recent detail this client
   * served for the flag, and only when it carries an experiment block: the
   * assignment the application acted on is the one analysis counts, whatever
   * document has arrived since. A flag never evaluated, a local override, a
   * fallback and stage `built_in` emit nothing and say so at debug level.
   */
  exposure(key: string): void {
    try {
      if (this.localOverride(key)) {
        this.log("debug", "exposure_skipped", { flag: key, cause: "local_override" });
        return;
      }
      const served = this.served.get(key);
      if (!served) {
        this.log("debug", "exposure_skipped", { flag: key, cause: this.values ? "no_experiment" : "no_values" });
        return;
      }
      this.emitExposure(key, served);
    } catch (e) {
      this.log("error", "evaluation_failed", { operation: "exposure", flag: key, cause: causeOf(e) });
    }
  }

  /**
   * Records an alias linking one unit to another, typically an anonymous ID
   * to a user ID at sign-up. The context is unchanged: keep the anonymous ID
   * in its units.
   */
  alias(link: AliasLink): void {
    try {
      this.record(aliasEvent(this.now(), this.random, link));
    } catch (e) {
      this.log("error", "evaluation_failed", { operation: "alias", cause: causeOf(e) });
    }
  }

  private emitExposure(key: string, d: Detail): void {
    const e = d.experiment;
    if (!e || this.options.sendExposures === false) return;
    // Repeats of one assignment are suppressed in this tab; the server
    // counts a unit's first exposure only.
    if (!this.exposed.admit(e)) return;
    this.record(
      exposureEvent(this.now(), this.random, {
        flagKey: key,
        experimentKey: e.key,
        variationKey: e.variation,
        unitType: e.unitType,
        unitId: e.unitId,
        platform: this.platform(),
        ...(this.options.appVersion !== undefined && { appVersion: this.options.appVersion }),
        // The experiment block is present with reason `experiment`, and
        // with reason `override` when an override's shadow evaluation lands
        // in the experiment.
        reason: d.reason === "override" ? "override" : "experiment",
        rulesetVersion: d.rulesetVersion ?? 0,
        override: e.override,
      }),
    );
  }

  private record(event: QueuedEvent): void {
    if (this.recorder) this.recorder.record(event);
    else this.ingest?.record(event);
  }

  // ------------------------------------------------------ the fallback chain

  /**
   * Accepts a values document from source; false, with `fetch_failed`, when
   * the fallback chain refuses it.
   */
  private accept(source: unknown, from: string, anyVersion = false): boolean {
    let doc: ValuesDocument;
    try {
      doc = ValuesDocument.parse(source);
      const held = this.values;
      if (!anyVersion && held && doc.rulesetVersion < held.rulesetVersion) {
        throw new Refused("version", `ruleset_version ${doc.rulesetVersion} is below the held ${held.rulesetVersion}`);
      }
    } catch (e) {
      this.refused(e, from);
      return false;
    }
    const changed = ValuesDocument.changedKeys(this.values, doc);
    this.values = doc;
    this.receivedAt = new Date(this.now());
    this.staleSince = undefined;
    this.log("info", "values_loaded", { source: from, ruleset_version: doc.rulesetVersion, flags: doc.flags.size });
    for (const ignored of doc.ignoredAttributes) {
      this.once(`attr:${ignored.name}`, "warn", "attribute_ignored", { name: ignored.name, code: ignored.code });
    }
    const dropped = this.refuseLocalOverrides(doc);
    this.setStage("fresh");
    this.markReady();
    this.fireChange(union(changed, dropped));
    return true;
  }

  private refused(e: unknown, from: string): void {
    if (e instanceof Refused) {
      if (e.refusal === "schema") {
        this.once("schema", "error", "schema_unsupported", { schema_version: e.schemaVersion, supported: SUPPORTED_SCHEMA_VERSIONS });
      }
      this.failed({ source: from, cause: e.refusal, detail: e.detail });
      return;
    }
    this.failed({ source: from, cause: "parse", detail: causeOf(e) });
  }

  /** Records a failure: the held values keep serving, `fresh` becomes `last_good`. */
  private failed(fields: Record<string, unknown>): void {
    this.staleSince ??= new Date(this.now());
    this.log("warn", "fetch_failed", fields);
    if (this.currentStage === "fresh") this.setStage("last_good");
  }

  private setStage(stage: Stage): void {
    const from = this.currentStage;
    if (from === stage) return;
    this.currentStage = stage;
    this.log(from === "fresh" ? "warn" : "info", "stage_changed", { from, to: stage });
    for (const l of [...this.stageListeners]) this.call(() => { l(stage); });
  }

  private markReady(): void {
    this.isReady = true;
    for (const w of this.readyWaiters.splice(0)) w();
  }

  /** @internal */
  fireChange(keys: readonly string[]): void {
    if (keys.length === 0) return;
    for (const l of [...this.changeListeners]) this.call(() => { l([...keys]); });
  }

  private call(fn: () => void): void {
    try {
      fn();
    } catch (e) {
      this.log("error", "listener_failed", { cause: causeOf(e) });
    }
  }

  // ------------------------------------------------------------- fetching

  private onVisible(): void {
    if (this.closed || this.visible) return;
    this.visible = true;
    this.ingest?.setVisible(true);
    this.refetch("visible");
    if (!this.inFlight) this.armPoll();
  }

  private onHidden(): void {
    if (this.closed) return;
    // Every hide hands the queue to the beacon, `pagehide` after a
    // `visibilitychange` included: the page may never run again.
    this.ingest?.beacon();
    if (!this.visible) return;
    this.visible = false;
    this.ingest?.setVisible(false);
    // The interval stops while the tab is hidden; a retry does not, so a
    // failed evaluation keeps its backoff instead of waiting a whole
    // interval: a failed fetch is retried by backoff alone.
    if (!this.retrying) {
      this.clock.clearTimeout(this.nextAttempt);
      this.nextAttempt = undefined;
    }
  }

  private onAnnounced(version: number): void {
    if (version <= (this.values?.rulesetVersion ?? -1) || version <= this.announced) return;
    this.announced = version;
    this.refetch("stream");
  }

  /** Runs start, which starts an evaluation, and resolves once an evaluation started no earlier than now has settled. */
  private attemptAfter(start: () => void): Promise<Stage> {
    const minSeq = this.attemptSeq + 1;
    const waited = new Promise<Stage>((resolve) => this.attemptWaiters.push({ minSeq, resolve }));
    start();
    return waited;
  }

  /**
   * Starts one evaluation, coalescing every trigger into the request in
   * flight: a further trigger only marks that another is due once it returns.
   */
  private refetch(trigger: Trigger): void {
    if (this.closed || this.offline || this.deferred) return;
    if (this.inFlight) {
      this.again = true;
      return;
    }
    // Visibility and the network coming back are throttled; what the
    // application asked for is not.
    if ((trigger === "visible" || trigger === "online") && this.now() - this.lastAttemptAt < this.timing.minRefetchGap) return;
    this.clock.clearTimeout(this.nextAttempt);
    this.nextAttempt = undefined;
    void this.run(trigger);
  }

  private async run(trigger: Trigger): Promise<void> {
    this.inFlight = true;
    this.started = true;
    this.lastAttemptAt = this.now();
    const seq = ++this.attemptSeq;
    const context = this.context;
    let ok = false;
    try {
      ok = await this.attempt(trigger, context);
    } catch (e) {
      // Reading the body of a request close aborted throws; that is no failure.
      if (!this.closed) {
        this.log("error", "evaluation_failed", { operation: "fetch", cause: causeOf(e) });
        this.failed({ source: "network", cause: "internal", detail: causeOf(e) });
      }
    } finally {
      this.inFlight = false;
      this.traceEvent?.("fetch_handled");
    }
    this.resolve(seq);
    if (this.closed) return;
    const again = this.again;
    this.again = false;
    if (ok) {
      this.retrying = false;
      this.backoff.reset();
    }
    if (again) {
      void this.run("context");
      return;
    }
    if (ok) {
      this.armPoll();
      return;
    }
    // A failed evaluation is retried by backoff alone; the poll waits.
    this.retrying = true;
    const delay = this.backoff.next(this.unauthorized ? this.timing.unauthorizedMax : this.timing.retryMax);
    this.nextAttempt = this.clock.setTimeout(() => {
      this.nextAttempt = undefined;
      this.retrying = false;
      this.refetch("retry");
    }, delay);
  }

  private armPoll(): void {
    // A retry owns the next attempt until it succeeds; the interval waits.
    if (this.retrying) return;
    this.clock.clearTimeout(this.nextAttempt);
    this.nextAttempt = undefined;
    if (this.closed || !this.visible || this.inFlight) return;
    this.nextAttempt = this.clock.setTimeout(() => {
      this.nextAttempt = undefined;
      this.refetch("interval");
    }, this.timing.pollInterval);
  }

  private resolve(seq: number): void {
    for (let i = this.attemptWaiters.length - 1; i >= 0; i--) {
      const w = this.attemptWaiters[i];
      if (w && w.minSeq <= seq) {
        this.attemptWaiters.splice(i, 1);
        this.call(() => { w.resolve(this.currentStage); });
      }
    }
  }

  /** One remote evaluation and what its response means. Returns whether it succeeded. */
  private async attempt(trigger: Trigger, context: Context): Promise<boolean> {
    this.unauthorized = false;
    const controller = new AbortController();
    const timer = this.clock.setTimeout(() => {
      controller.abort(new Error(`timed out after ${this.timing.requestTimeout} ms`));
    }, this.timing.requestTimeout);
    this.abortInFlight = () => { controller.abort(new Error("client closed")); };
    let res: Response;
    let text: string;
    try {
      try {
        // Only headers the server's CORS preflight allows: a browser would
        // block anything else, the User-Agent this SDK would like to send
        // included, so the context's attributes carry its identity.
        res = await this.doFetch(`${this.baseUrl}${SDK_API_PATH}/evaluate`, {
          method: "POST",
          headers: {
            Authorization: `Bearer ${this.sdkKey}`,
            "Content-Type": "application/json",
            "Flagnetic-Schema-Version": String(SCHEMA_VERSION),
          },
          body: JSON.stringify({ context: requestContext(context, this.options) }),
          signal: controller.signal,
        });
      } catch (e) {
        // A closed client records no failure: it no longer fetches.
        if (this.closed) return false;
        const reason: unknown = controller.signal.aborted ? (controller.signal.reason as unknown) : e;
        this.failed({ source: "network", cause: "network", trigger, detail: causeOf(reason) });
        return false;
      }
      if (res.status !== 200) {
        if (res.status === 401 || res.status === 403) {
          this.unauthorized = true;
          this.once("revoked", "error", "key_revoked", { status: res.status });
          // The change stream carries the same key: it stays closed until
          // an evaluation succeeds again.
          this.stream?.close();
        }
        this.failed({ source: "network", cause: "status", status: res.status, trigger });
        return false;
      }
      text = await res.text();
    } finally {
      this.clock.clearTimeout(timer);
      this.abortInFlight = undefined;
    }
    if (this.closed) return false;
    // The context changed while this request was in flight: its answer is
    // for the previous context, and the evaluation of the new one follows,
    // so it neither serves nor counts as a failure.
    if (context !== this.context) return false;
    if (!this.accept(text, "network", context !== this.acceptedContext)) return false;
    this.acceptedContext = context;
    // The server answers again, so a stream a refusal closed comes back.
    this.logged.delete("revoked");
    if (this.stream && !this.stream.running) this.stream.open();
    this.persist({ ...(context.units ?? {}) });
    return true;
  }

  private platform(): string {
    return this.options.platform ?? "browser";
  }

  // ---------------------------------------------------------------- store

  private loadStored(): void {
    const key = this.keys.lastGood(this.context.units ?? {});
    const raw = this.store.get(key);
    if (raw === null) return;
    try {
      const entry = JSON.parse(raw) as { units?: unknown; received_at?: unknown; values?: unknown };
      const units = entry.units;
      if (typeof units !== "object" || units === null || !sameUnits(units as Record<string, string>, this.context.units ?? {})) {
        throw new Error("not a last good entry of this unit set");
      }
      const doc = ValuesDocument.parse(entry.values);
      this.values = doc;
      this.receivedAt = typeof entry.received_at === "string" ? new Date(entry.received_at) : new Date(this.now());
      this.log("info", "values_loaded", { source: "store", ruleset_version: doc.rulesetVersion, flags: doc.flags.size });
      this.setStage("last_good");
    } catch (e) {
      this.log("warn", "store_failed", { operation: "read", key, cause: causeOf(e) });
    }
  }

  /** Keeps the most recent unit sets, so the anonymous and the signed-in state each retain theirs. */
  private persist(units: Record<string, string>): void {
    const values = this.values;
    const receivedAt = this.receivedAt;
    if (!values || !receivedAt) return;
    const key = this.keys.lastGood(units);
    this.store.set(
      key,
      JSON.stringify({ units, ruleset_version: values.rulesetVersion, received_at: receivedAt.toISOString(), values: values.raw }),
    );
    let keys: string[] = [];
    const index = this.store.get(this.keys.index);
    if (index !== null) {
      try {
        const parsed: unknown = JSON.parse(index);
        if (Array.isArray(parsed)) keys = (parsed as unknown[]).filter((k): k is string => typeof k === "string");
      } catch {
        // A torn index costs the other unit sets their entries, nothing more.
      }
    }
    keys = [key, ...keys.filter((k) => k !== key)];
    this.store.set(this.keys.index, JSON.stringify(keys.slice(0, this.timing.storedUnitSets)));
    for (const old of keys.slice(this.timing.storedUnitSets)) this.store.remove(old);
  }

  // ------------------------------------------------------- local overrides

  /** @internal The values held now, for local overrides and the offline helpers. */
  heldValues(): ValuesDocument | undefined {
    return this.values;
  }

  /** @internal Serves an offline document; the version never has to rise. */
  loadOffline(source: unknown): void {
    this.accept(source, "offline", true);
  }

  /** @internal */
  offlineRecorder(): Recorder {
    return this.recorder ?? new Recorder();
  }

  private localOverride(key: string): { readonly type: FlagType; readonly value: JsonValue } | undefined {
    if (this.localOverrides.size === 0 || this.values?.protectedEnvironment === true) return undefined;
    return this.localOverrides.get(key);
  }

  // Drops every local override when doc belongs to a protected environment.
  private refuseLocalOverrides(doc: ValuesDocument): string[] {
    if (!doc.protectedEnvironment || this.localOverrides.size === 0) return [];
    const keys = [...this.localOverrides.keys()].sort();
    this.localOverrides = new Map();
    for (const k of keys) this.log("warn", "override_refused", { flag: k, cause: "protected_environment" });
    return keys;
  }

  // -------------------------------------------------------------- logging

  /** @internal */
  log(level: LogLevel, event: LogEvent, fields: Record<string, unknown>): void {
    safeLog(this.logger, level, event, fields);
  }

  private once(id: string, level: LogLevel, event: LogEvent, fields: Record<string, unknown>): void {
    if (this.logged.has(id)) return;
    this.logged.add(id);
    this.log(level, event, fields);
  }
}

/**
 * Local overrides: substitutions of flag values inside this browser that
 * never reach the server. An overridden flag reads reason `local_override`
 * with no experiment block and the stage unchanged, and `exposure` is a
 * no-op for it. They exist only with the `allowOverrides` option and are
 * refused in a protected environment.
 */
export class LocalOverrides {
  /** @internal */
  constructor(private readonly c: FlagneticClient) {}

  /**
   * Substitutes value for the flag key and fires the change listeners.
   * Throws LocalOverridesNotAllowed without the `allowOverrides` option and
   * LocalOverrideRefused when the held values name a protected environment,
   * logging `override_refused` either way. The value must be of the flag's
   * type; a key the values lack takes the type of the value. Throws a
   * TypeError for a value of another type.
   */
  set(key: string, value: JsonValue): void {
    const c = this.c;
    if (!c.allowOverrides) {
      c.log("warn", "override_refused", { flag: key, cause: "not_allowed" });
      throw new LocalOverridesNotAllowed();
    }
    const values = c.heldValues();
    if (values?.protectedEnvironment === true) {
      c.log("warn", "override_refused", { flag: key, cause: "protected_environment" });
      throw new LocalOverrideRefused(key);
    }
    const type = values?.flags.get(key)?.type ?? flagTypeOf(value);
    if (!fitsFlagType(type, value)) {
      throw new TypeError(`flagnetic: a local override of ${JSON.stringify(key)} must be a ${type} value`);
    }
    const next = new Map(c.localOverrides);
    next.set(key, { type, value: type === "json" ? copyJson(value) : value });
    c.localOverrides = next;
    c.log("warn", "override_set", { flag: key });
    c.fireChange([key]);
  }

  /** Drops the local override of key, firing the change listeners when there was one. */
  remove(key: string): void {
    const c = this.c;
    if (!c.localOverrides.has(key)) return;
    const next = new Map(c.localOverrides);
    next.delete(key);
    c.localOverrides = next;
    c.fireChange([key]);
  }

  /** Drops every local override, firing the change listeners with their keys. */
  clear(): void {
    const c = this.c;
    const keys = [...c.localOverrides.keys()].sort();
    c.localOverrides = new Map();
    c.fireChange(keys);
  }

  /** The local overrides by flag key. */
  list(): Record<string, JsonValue> {
    const out: Record<string, JsonValue> = {};
    for (const [k, o] of this.c.localOverrides) out[k] = o.value;
    return out;
  }
}

/**
 * The test helpers of an offline client: no network at all, stage `fresh`,
 * exposures and aliases recorded in memory.
 */
export class OfflineMode {
  /** @internal */
  constructor(
    private readonly c: FlagneticClient,
    private readonly source: ValuesSource | (() => ValuesSource),
  ) {}

  /** The exposures and aliases the client emitted, oldest first. */
  recorded(): QueuedEvent[] {
    return this.c.offlineRecorder().recorded();
  }

  /**
   * Serves value for the flag key, as if the server had evaluated it, and
   * fires the change listeners. The flag takes the type of value and no
   * variation; it keeps its reason when its type is unchanged, else (and for
   * reason `error`) reads `targeting_match`.
   */
  set(key: string, value: JsonValue): void {
    const held = this.c.heldValues();
    const raw: Record<string, unknown> = held
      ? (JSON.parse(JSON.stringify(held.raw)) as Record<string, unknown>)
      : { schema_version: SCHEMA_VERSION, ruleset_version: 0, flags: {} };
    const flags = (raw.flags ?? {}) as Record<string, unknown>;
    const type = flagTypeOf(value);
    const existing = flags[key];
    const reason =
      typeof existing === "object" && existing !== null && (existing as { type?: unknown }).type === type
        ? (existing as { reason?: unknown }).reason
        : undefined;
    flags[key] = { type, value, reason: typeof reason === "string" && reason !== "error" ? reason : "targeting_match" };
    raw.flags = flags;
    this.c.loadOffline(raw);
  }

  /** Reads the source again and serves it; changed keys fire the change listeners. */
  reload(): void {
    try {
      this.c.loadOffline(typeof this.source === "function" ? this.source() : this.source);
    } catch (e) {
      this.c.log("warn", "fetch_failed", { source: "offline", cause: "parse", detail: causeOf(e) });
    }
  }
}

/**
 * The context posted: the standard attributes the SDK knows unless the
 * context sets them. `sdk`, `sdk_version` and `platform` name this SDK to
 * the server, which cannot read a browser's User-Agent as an SDK's.
 */
function requestContext(context: Context, options: ClientOptions): { units: Record<string, string>; attributes: Record<string, unknown> } {
  return {
    units: { ...(context.units ?? {}) },
    attributes: {
      platform: options.platform ?? "browser",
      ...(options.appVersion !== undefined && { app_version: options.appVersion }),
      sdk: SDK_NAME,
      sdk_version: VERSION,
      ...(context.attributes ?? {}),
    },
  };
}

function union(a: readonly string[], b: readonly string[]): string[] {
  return [...new Set([...a, ...b])].sort();
}

function safeVisible(page: Page): boolean {
  try {
    return page.visible();
  } catch {
    return true;
  }
}
