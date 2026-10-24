// The server-mode client: holds a ruleset in memory and evaluates locally.
// Delivery fetches and streams; this module owns the fallback chain's stage,
// the getters, local overrides and the listeners.
import { decodeRuleset, type Context, type Result, type Ruleset } from "../evaluator/evaluate.js";
import type { JsonValue } from "../evaluator/ruleset.js";
import { fitsFlagType, flagTypeOf } from "../internal/detail.js";
import { aliasEvent, contextFingerprint, exposureEvent, ExposureSuppressor, ServedDetails, type AliasLink, type QueuedEvent } from "../internal/events.js";
import { changedFlags } from "./changes.js";
import { defaultTiming, Delivery, realClock, type Clock, type Timing, type TraceEvent } from "./delivery.js";
import { EventQueue } from "./events.js";
import { Ingest } from "./ingest.js";
import { causeOf, consoleLogger, safeLog } from "./logger.js";
import { Recorder } from "./recorder.js";
import { Summaries } from "./summaries.js";
import { readStore, writeStore } from "./store.js";
import type {
  Detail,
  DetailErrorCode,
  DetailReason,
  Experiment,
  FlagType,
  LogEvent,
  Logger,
  LogLevel,
  RulesetSource,
  ServerOptions,
  Stage,
} from "./types.js";
import { valuesDocument, type ValuesDocument, type ValuesOptions } from "./values.js";

/** The lowest safety poll interval the options accept, in milliseconds. */
const pollFloor = 30_000;

/** The timings of events and summaries in server mode. */
export interface EventTiming {
  /** Milliseconds between flushes of the exposure and alias queue. */
  readonly flushInterval: number;
  /** The most events one batch carries, and the size that flushes at once. */
  readonly batchSize: number;
  /** The most events the queue holds before dropping its oldest. */
  readonly capacity: number;
  /** Milliseconds between evaluation summaries. */
  readonly summaryInterval: number;
  /** How old a failed window's interval may be before it is dropped rather than merged forward. */
  readonly maxWindowAge: number;
  /** How long close waits for the last flush. */
  readonly closeTimeout: number;
  /** The most (experiment, unit type, unit ID, variation) the exposure suppressor holds. */
  readonly exposureMemory: number;
  /** The most (flag, context) assignments an exposure call can still look up. */
  readonly detailMemory: number;
}

export const defaultEventTiming: EventTiming = {
  flushInterval: 2_000,
  batchSize: 500,
  capacity: 10_000,
  summaryInterval: 60_000,
  maxWindowAge: 3_600_000,
  closeTimeout: 5_000,
  exposureMemory: 1_000,
  detailMemory: 10_000,
};

/** A served value and whether its flag records an exposure by itself. */
interface Served<T extends JsonValue = JsonValue> {
  readonly d: Detail<T>;
  readonly automaticExposure: boolean;
}

/** A served value from a path where no experiment can be in play. */
function served<T extends JsonValue>(d: Detail<T>): Served<T> {
  return { d, automaticExposure: false };
}

interface Held {
  readonly rs: Ruleset;
  readonly receivedAt: Date;
}

/** Test seams: timings, the random source, the clock, fetch and a trace of what delivery handled. */
export interface Internals {
  readonly timing?: Partial<Timing>;
  readonly eventTiming?: Partial<EventTiming>;
  readonly random?: () => number;
  readonly clock?: Clock;
  /** The wall clock behind event timestamps and event IDs. */
  readonly now?: () => number;
  readonly fetch?: typeof fetch;
  readonly trace?: (event: TraceEvent) => void;
}

/** Where the fallback chain stands. */
export interface Status {
  readonly stage: Stage;
  /** Since when the most recent attempts have been failing; absent after a success. */
  readonly staleSince?: Date;
  /** The version of the held ruleset; absent at stage `built_in`. */
  readonly rulesetVersion?: number;
  /** When the held ruleset was received. */
  readonly receivedAt?: Date;
}

const flagTypes: ReadonlySet<string> = new Set<FlagType>(["boolean", "string", "number", "json"]);

/**
 * Constructs a server-mode client for a server key and the platform's base
 * URL. It fails loudly only for a client key (`fn_cli_`), which belongs to
 * `flagnetic/client`; network trouble never makes it throw. With the
 * `offline` option the key and URL are ignored and the document is decoded
 * at once; an unreadable offline document throws.
 */
export function createServer(sdkKey: string, baseUrl: string, options: ServerOptions = {}): FlagneticServer {
  return new FlagneticServer(sdkKey, baseUrl, options, {});
}

/** createServer with test seams; not part of the public entry point. */
export function createServerWith(sdkKey: string, baseUrl: string, options: ServerOptions, internals: Internals): FlagneticServer {
  return new FlagneticServer(sdkKey, baseUrl, options, internals);
}

/**
 * A server-mode client. Getters evaluate the held ruleset for the context
 * passed in, are total (they never throw) and serve the built-in default
 * with reason `fallback` whenever there is nothing to serve.
 */
export class FlagneticServer {
  /** The in-memory recorder of an offline client; undefined online. TestServer makes it public. */
  protected readonly recorder: Recorder | undefined;
  /** Local overrides; they apply only with the `allowOverrides` option. */
  readonly overrides: LocalOverrides;

  private held: Held | undefined;
  private currentStage: Stage = "built_in";
  private staleSince: Date | undefined;
  private readonly logger: Logger;
  private readonly source: (() => RulesetSource) | undefined;
  private readonly delivery: Delivery | undefined;
  private readonly storePath: string | undefined;
  private storeWrites: Promise<void> = Promise.resolve();
  private readonly changeListeners = new Set<(keys: string[]) => void>();
  private readonly stageListeners = new Set<(stage: Stage) => void>();
  private readyWaiters: (() => void)[] = [];
  private isReady = false;
  private closed = false;
  private readonly logged = new Set<string>();
  /** Reports evaluation summaries; undefined offline and with `sendSummaries` off. */
  private readonly summaries: Summaries | undefined;
  /** Queues exposures and aliases; undefined offline and with `sendExposures` off. */
  private readonly queue: EventQueue | undefined;
  private readonly exposed: ExposureSuppressor;
  /** The assignment every flag last served, by context: what an exposure reports. */
  private readonly served: ServedDetails;
  /** The wall clock behind event timestamps and event IDs. */
  private readonly now: () => number;
  private readonly random: () => number;
  private readonly eventTiming: EventTiming;
  private readonly sendExposures: boolean;
  private readonly platform: string;
  private readonly appVersion: string | undefined;
  /** @internal */
  readonly allowOverrides: boolean;
  /** @internal */
  localOverrides = new Map<string, { readonly type: FlagType; readonly value: JsonValue }>();

  /** @internal Use createServer. */
  constructor(sdkKey: string, baseUrl: string, options: ServerOptions, internals: Internals) {
    this.logger = options.logger ?? consoleLogger("warn");
    this.allowOverrides = options.allowOverrides === true;
    this.overrides = new LocalOverrides(this);
    if (this.allowOverrides) this.log("warn", "local_overrides_allowed", {});
    this.eventTiming = {
      ...defaultEventTiming,
      ...(options.flushInterval !== undefined && { flushInterval: options.flushInterval }),
      ...(options.summaryInterval !== undefined && { summaryInterval: options.summaryInterval }),
      ...internals.eventTiming,
    };
    this.now = internals.now ?? Date.now;
    this.random = internals.random ?? Math.random;
    this.exposed = new ExposureSuppressor(this.eventTiming.exposureMemory);
    this.served = new ServedDetails(this.eventTiming.detailMemory);
    this.sendExposures = options.sendExposures !== false;
    this.platform = options.platform ?? "node";
    this.appVersion = options.appVersion;
    if (!this.sendExposures) this.log("warn", "exposures_off", {});

    if (options.offline !== undefined) {
      const offline = options.offline;
      this.source = typeof offline === "function" ? (offline as () => RulesetSource) : () => offline;
      this.recorder = new Recorder();
      this.load(decodeRuleset(this.source()), "offline");
      return;
    }
    if (typeof sdkKey === "string" && sdkKey.startsWith("fn_cli_")) {
      throw new Error("flagnetic/server needs a server key (fn_srv_…); a client key belongs to flagnetic/client");
    }
    const timing: Timing = {
      ...defaultTiming,
      ...(options.pollInterval !== undefined && { pollConnected: Math.max(pollFloor, options.pollInterval) }),
      ...(options.pollIntervalWithoutStream !== undefined && {
        pollDisconnected: Math.max(pollFloor, options.pollIntervalWithoutStream),
      }),
      ...(options.requestTimeout !== undefined && { requestTimeout: options.requestTimeout }),
      ...internals.timing,
    };
    this.storePath = options.store;
    this.delivery = new Delivery(
      {
        sdkKey,
        baseUrl,
        platform: options.platform ?? "node",
        sse: options.stream !== false,
        timing,
        random: internals.random ?? Math.random,
        clock: internals.clock ?? realClock,
        fetch: internals.fetch ?? ((input, init) => fetch(input, init)),
        ...(internals.trace && { trace: internals.trace }),
      },
      {
        heldVersion: () => this.held?.rs.version,
        accept: (rs, text) => {
          this.staleSince = undefined;
          this.load(rs, "network");
          this.persist(text);
        },
        notModified: () => {
          this.staleSince = undefined;
          this.setStage("fresh");
          this.markReady();
        },
        failed: () => {
          this.staleSince ??= new Date();
          if (this.currentStage === "fresh") this.setStage("last_good");
        },
        log: (level, event, fields) => {
          this.log(level, event, fields);
        },
      },
    );
    const now = this.now;
    const random = this.random;
    const clock = internals.clock ?? realClock;
    const ingest = new Ingest({
      baseUrl,
      sdkKey,
      platform: this.platform,
      requestTimeout: timing.requestTimeout,
      clock,
      now,
      fetch: internals.fetch ?? ((input, init) => fetch(input, init)),
    });
    const trace = internals.trace;
    const handled = trace ? (): void => { trace({ kind: "events_handled" }); } : undefined;
    const log = (level: LogLevel, event: LogEvent, fields: Record<string, unknown>): void => { this.log(level, event, fields); };
    if (options.sendSummaries !== false) {
      this.summaries = new Summaries(
        {
          interval: this.eventTiming.summaryInterval,
          maxWindowAge: this.eventTiming.maxWindowAge,
          retryInitial: timing.retryInitial,
          retryMax: timing.retryMax,
          unauthorizedMax: timing.unauthorizedMax,
          clock,
          now,
          random,
          ...(this.appVersion !== undefined && { appVersion: this.appVersion }),
          rulesetVersion: () => this.held?.rs.version,
        },
        ingest,
        log,
        handled,
      );
      this.summaries.start();
    }
    if (this.sendExposures) {
      this.queue = new EventQueue(
        {
          capacity: this.eventTiming.capacity,
          batchSize: this.eventTiming.batchSize,
          flushInterval: this.eventTiming.flushInterval,
          retryInitial: timing.retryInitial,
          retryMax: timing.retryMax,
          unauthorizedMax: timing.unauthorizedMax,
          clock,
          random,
        },
        ingest,
        log,
        handled,
      );
      this.queue.start();
    }
    void this.start();
  }

  private async start(): Promise<void> {
    if (this.storePath !== undefined) {
      try {
        const stored = await readStore(this.storePath);
        if (stored && !this.held && !this.closed) {
          const rs = decodeRuleset(stored.document);
          this.swap(rs, stored.receivedAt, "store");
          this.setStage("last_good");
          this.fireChange(rs.document.flags.map((f) => f.key).sort());
        }
      } catch (e) {
        this.log("warn", "store_failed", { operation: "read", cause: causeOf(e) });
      }
    }
    if (!this.closed) await this.delivery?.start();
  }

  private persist(text: string): void {
    const path = this.storePath;
    if (path === undefined) return;
    // One write at a time, in the order the rulesets were accepted.
    this.storeWrites = this.storeWrites
      .then(() => writeStore(path, text))
      .catch((e: unknown) => {
        this.log("warn", "store_failed", { operation: "write", cause: causeOf(e) });
      });
  }

  // ------------------------------------------------------------ lifecycle

  /** The current stage of the fallback chain. */
  get stage(): Stage {
    return this.currentStage;
  }

  /** The stage, since when attempts have been failing, and the held ruleset's version and age. */
  status(): Status {
    const held = this.held;
    return {
      stage: this.currentStage,
      ...(this.staleSince && { staleSince: new Date(this.staleSince) }),
      ...(held && { rulesetVersion: held.rs.version, receivedAt: new Date(held.receivedAt) }),
    };
  }

  /**
   * Resolves with the stage reached once the first ruleset arrives (a 304
   * included), at the timeout in milliseconds, or at close. Never rejects.
   * An offline client is ready at once.
   */
  ready(timeout?: number): Promise<Stage> {
    if (this.isReady || this.closed) return Promise.resolve(this.currentStage);
    return new Promise((resolve) => {
      let timer: ReturnType<typeof setTimeout> | undefined;
      const done = (): void => {
        clearTimeout(timer);
        this.readyWaiters = this.readyWaiters.filter((w) => w !== done);
        resolve(this.currentStage);
      };
      this.readyWaiters.push(done);
      if (timeout !== undefined) {
        timer = setTimeout(done, Math.max(0, timeout));
        // A pending ready never keeps a Node process alive.
        (timer as { unref?: () => void }).unref?.();
      }
    });
  }

  /**
   * Registers listener, called with the sorted keys of the flags whose value
   * may have changed: on every accepted ruleset (through segments,
   * declarations and prerequisites as well as their own configuration) and
   * on every local override change. Returns a function that unregisters it.
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
   * Reports the evaluation summary of the window so far and sends the
   * queued exposures and aliases, waiting at most timeout milliseconds
   * (5 s by default); what is not yet sent goes on in the background. Never
   * rejects; an offline client resolves at once, having recorded them
   * instead.
   */
  async flush(timeout: number = this.eventTiming.closeTimeout): Promise<void> {
    const flushed = Promise.all([this.summaries?.flush(), this.queue?.flush()]).then(
      () => undefined,
      (e: unknown) => {
        this.log("error", "evaluation_failed", { operation: "flush", cause: causeOf(e) });
      },
    );
    await Promise.race([flushed, deadline(timeout)]);
  }

  /**
   * Stops the stream and polling, resolves ready waiters and flushes what
   * is queued within the close timeout, 5 s by default. A flush waiting out
   * a backoff does not hold it up: the batch goes once more and close
   * returns. Getters keep serving the held ruleset.
   */
  async close(): Promise<void> {
    if (this.closed) return;
    this.closed = true;
    this.delivery?.close();
    this.markReady();
    const flushed = Promise.all([this.summaries?.close(), this.queue?.close()]).catch((e: unknown) => {
      this.log("error", "evaluation_failed", { operation: "close", cause: causeOf(e) });
    });
    await Promise.race([flushed, deadline(this.eventTiming.closeTimeout)]);
  }

  /**
   * Re-reads the offline source and swaps the ruleset in, firing change
   * listeners. Throws, keeping the held ruleset, when the document cannot
   * be read. Only an offline client can reload; TestServer makes it public.
   */
  protected reload(): void {
    if (!this.source) throw new Error("flagnetic: reload needs an offline client");
    this.load(decodeRuleset(this.source()), "offline");
  }

  // ------------------------------------------------- server rendering

  /**
   * Evaluates every flag of the held ruleset for context into a values
   * document: what remote evaluation serves a client key, ready to be
   * serialised into the page it rendered and handed to `createClient`'s
   * `bootstrap`, so the browser hydrates with the values the page was
   * rendered from. The document is masked: where targeting decided a value
   * its reason is `targeting_match` and no rule is named, because a page
   * is public. Read the true reason from `getDetail`.
   *
   * Undefined at stage `built_in`: there is nothing to render from, the page
   * serves built-in defaults, and the browser that hydrates it does the same
   * until its own client has values. Local overrides take no part — they are
   * a substitution inside this process and never reach a browser. Never
   * throws.
   */
  values(context: Context, options: ValuesOptions = {}): ValuesDocument | undefined {
    try {
      const held = this.held;
      if (!held) return undefined;
      // Rendering a page is evaluating: a flag served only from here must
      // not read as stale in the inventory, and it is counted with its true
      // reason, not the masked one the page carries. A flag that served
      // nothing is counted where it is read, as `fallback`.
      return valuesDocument(held.rs, isObject(context) ? context : {}, options, (key, r) => {
        if (r.reason !== "error") this.count(key, r.variation, r.reason);
      });
    } catch (e) {
      this.log("error", "evaluation_failed", { operation: "values", cause: causeOf(e) });
      return undefined;
    }
  }

  // ------------------------------------------------------------- getters

  /** Serves a boolean flag for context, or defaultValue with reason `fallback`. */
  getBool(context: Context, key: string, defaultValue: boolean): boolean {
    return this.evaluate(key, "boolean", defaultValue, context).value;
  }

  /** Serves a string flag for context, or defaultValue with reason `fallback`. */
  getString(context: Context, key: string, defaultValue: string): string {
    return this.evaluate(key, "string", defaultValue, context).value;
  }

  /** Serves a number flag for context, or defaultValue with reason `fallback`. */
  getNumber(context: Context, key: string, defaultValue: number): number {
    return this.evaluate(key, "number", defaultValue, context).value;
  }

  /** Serves a JSON flag for context, frozen, or defaultValue with reason `fallback`. */
  getJson(context: Context, key: string, defaultValue: JsonValue): JsonValue {
    return this.evaluate(key, "json", defaultValue, context).value;
  }

  /**
   * Serves a flag for context with why it was served. The type of
   * defaultValue names the flag type asked for: boolean, string, number, or
   * anything else as JSON.
   */
  getDetail(context: Context, key: string, defaultValue: boolean): Detail<boolean>;
  getDetail(context: Context, key: string, defaultValue: string): Detail<string>;
  getDetail(context: Context, key: string, defaultValue: number): Detail<number>;
  getDetail(context: Context, key: string, defaultValue: JsonValue): Detail;
  getDetail(context: Context, key: string, defaultValue: JsonValue): Detail {
    return this.evaluate(key, flagTypeOf(defaultValue), defaultValue, context);
  }

  // The API boundary every getter goes through: it never throws, and an
  // offline client counts what it served.
  private evaluate<T extends JsonValue>(key: string, type: FlagType, def: T, context: Context): Detail<T> {
    let served: Served<T>;
    try {
      served = this.detail(key, type, def, context);
    } catch (e) {
      this.log("error", "evaluation_failed", { flag: key, cause: causeOf(e) });
      served = { d: this.fallback(def, "general"), automaticExposure: false };
    }
    const { d, automaticExposure } = served;
    this.count(key, d.variation, d.reason);
    try {
      // What an exposure call reports is what was served here, not what the
      // ruleset would say by the time it is called. A flag that
      // stops carrying an experiment block forgets what it served, so the
      // call reports nothing rather than an assignment the ruleset no
      // longer serves.
      const fingerprint = contextFingerprint(context);
      if (d.experiment) {
        this.served.remember(key, fingerprint, { experiment: d.experiment, reason: d.reason, rulesetVersion: d.rulesetVersion ?? 0 });
      } else {
        this.served.forget(key, fingerprint, d.rulesetVersion ?? 0);
      }
      // Automatic exposure is the flag's own setting, carried on the result:
      // every evaluation with an experiment block emits, as if `exposure` had
      // been called. A local override and a fallback carry none.
      if (d.experiment && automaticExposure) this.emit(key, d, context);
    } catch (e) {
      this.log("error", "evaluation_failed", { operation: "automatic_exposure", flag: key, cause: causeOf(e) });
    }
    return d;
  }

  // count records one served value in the evaluation summary, or in the
  // recorder an offline client keeps instead.
  private count(flag: string, variation: string | undefined, reason: DetailReason): void {
    this.recorder?.count(flag, variation, reason);
    this.summaries?.count(flag, variation, reason);
  }

  private detail<T extends JsonValue>(key: string, type: FlagType, def: T, context: Context): Served<T> {
    const held = this.held;
    const local = this.localOverride(key);
    if (local) {
      if (local.type !== type) return served(this.mismatch(key, local.type, type, def));
      return served({ ...this.base(), value: local.value as T, reason: "local_override" });
    }
    if (!held) return served(this.fallback(def, "no_ruleset"));
    // A flag of a type this SDK does not know is absent: a newer server may add types.
    const flag = held.rs.flag(key);
    if (!flag || !flagTypes.has(flag.type)) return served(this.fallback(def, "flag_not_found"));
    if (flag.type !== type) return served(this.mismatch(key, flag.type, type, def));
    const r = held.rs.evaluate(key, isObject(context) ? context : {});
    if (r.reason === "error") {
      const code = r.error_code ?? "inconsistent_ruleset";
      return served(this.fallback(def, code));
    }
    if (!fitsFlagType(type, r.value)) return served(this.fallback(def, "inconsistent_ruleset"));
    return {
      d: {
        ...this.base(),
        value: r.value as T,
        reason: r.reason,
        ...(r.variation !== undefined && { variation: r.variation }),
        ...(r.rule_id !== undefined && { ruleId: r.rule_id }),
        overridden: r.override_influenced,
        ...(r.experiment && { experiment: experimentOf(r.experiment) }),
      },
      automaticExposure: r.automatic_exposure === true,
    };
  }

  private base(): { stage: Stage; overridden: boolean; rulesetVersion?: number; receivedAt?: Date } {
    const held = this.held;
    return {
      stage: this.currentStage,
      overridden: false,
      ...(held && { rulesetVersion: held.rs.version, receivedAt: new Date(held.receivedAt) }),
    };
  }

  private fallback<T extends JsonValue>(def: T, code: DetailErrorCode): Detail<T> {
    return { ...this.base(), value: def, reason: "fallback", errorCode: code };
  }

  private mismatch<T extends JsonValue>(key: string, flagType: string, requested: FlagType, def: T): Detail<T> {
    this.once(`type:${key}`, "warn", "type_mismatch", { flag: key, flag_type: flagType, requested_type: requested });
    return this.fallback(def, "type_mismatch");
  }

  // -------------------------------------------------------------- events

  /**
   * Records that the unit of context actually encountered the behaviour the
   * flag's value decides. What is reported is the most recent detail this
   * client served for that flag and that context, and only when it carries
   * an experiment block (an override landing in a running experiment
   * included): the assignment the application acted
   * on is the one analysis counts, whatever ruleset has arrived since. A
   * local override, a fallback and a flag never evaluated for this context
   * emit nothing and say so at debug level. Repeats of one assignment are
   * suppressed in this process; the platform's deduplication and its first
   * exposure stay authoritative. Never throws.
   */
  exposure(context: Context, key: string): void {
    try {
      const skipped = (cause: string): void => { this.log("debug", "exposure_skipped", { flag: key, cause }); };
      if (this.localOverride(key)) {
        skipped("local_override");
        return;
      }
      const served = this.served.recall(key, contextFingerprint(context));
      if (!served) {
        // A flag no evaluation of which has ever carried an experiment
        // block has no running experiment, or the call names the wrong key;
        // one that has, but not for this context, is a call passing a
        // context the flag was not evaluated with.
        skipped(this.served.everCarried(key) ? "no_detail" : "no_experiment");
        return;
      }
      this.emit(key, { reason: served.reason as DetailReason, rulesetVersion: served.rulesetVersion, experiment: served.experiment }, context);
    } catch (e) {
      this.log("error", "evaluation_failed", { operation: "exposure", flag: key, cause: causeOf(e) });
    }
  }

  /**
   * Records an alias linking one unit to another, typically an anonymous ID
   * to a user ID at sign-up. It changes no context: keep the anonymous unit
   * ID in the contexts you evaluate with. Never throws.
   */
  alias(link: AliasLink): void {
    try {
      this.record(aliasEvent(this.now(), this.random, link));
    } catch (e) {
      this.log("error", "evaluation_failed", { operation: "alias", cause: causeOf(e) });
    }
  }

  // emit queues one exposure for a result that carries an experiment block.
  private emit(flag: string, d: Pick<Detail, "reason" | "rulesetVersion" | "experiment">, context: Context): void {
    const e = d.experiment;
    if (!e || !this.sendExposures) return;
    if (!this.exposed.admit(e)) return;
    const attributes = (isObject(context) ? (context as { attributes?: Readonly<Record<string, unknown>> }).attributes : undefined) ?? {};
    // Platform and app version come from the context's predeclared
    // attributes, and from the options where the context names neither.
    const platform = typeof attributes.platform === "string" ? attributes.platform : this.platform;
    const appVersion = typeof attributes.app_version === "string" ? attributes.app_version : this.appVersion;
    this.record(
      exposureEvent(this.now(), this.random, {
        flagKey: flag,
        experimentKey: e.key,
        variationKey: e.variation,
        unitType: e.unitType,
        unitId: e.unitId,
        platform,
        ...(appVersion !== undefined && { appVersion }),
        // The experiment block is present with reason `experiment`, and
        // with reason `override` when an override's shadow evaluation lands
        // in the experiment.
        reason: d.reason === "override" ? "override" : "experiment",
        rulesetVersion: d.rulesetVersion ?? 0,
        override: e.override,
      }),
    );
  }

  // record hands an event to the queue, or to the recorder an offline
  // client keeps instead. With exposures off nothing is kept at all.
  private record(event: QueuedEvent): void {
    if (!this.sendExposures) return;
    if (this.recorder) this.recorder.record(event);
    else this.queue?.record(event);
  }

  // ------------------------------------------------------------- loading

  // load swaps in a decoded ruleset: stage fresh, ready, change listeners.
  private load(rs: Ruleset, source: "offline" | "network"): void {
    const prev = this.held;
    this.swap(rs, new Date(), source);
    this.setStage("fresh");
    this.markReady();
    const dropped = this.refuseLocalOverrides(rs);
    const changed = prev ? changedFlags(prev.rs.document, rs.document) : rs.document.flags.map((f) => f.key);
    this.fireChange(union(changed, dropped));
  }

  private swap(rs: Ruleset, receivedAt: Date, source: string): void {
    this.held = { rs, receivedAt };
    this.log("info", "ruleset_loaded", { source, ruleset_version: rs.version, flags: rs.document.flags.length });
    this.reportUnsupported(rs);
  }

  // reportUnsupported logs rule_unsupported for every flag of rs holding a
  // construct the evaluator does not know, once per flag and ruleset
  // version: the evaluator finds them when it decodes, so a flag nobody
  // evaluates is reported too, and an SDK that must be upgraded says so
  // without a line per evaluation.
  private reportUnsupported(rs: Ruleset): void {
    const report = rs.unsupportedRules();
    for (const key of [...report.keys()].sort()) {
      const constructs = (report.get(key) ?? []).map((u) => `${u.kind} ${u.name}`);
      this.once(`rule:${key}:${String(rs.version)}`, "error", "rule_unsupported", {
        flag: key,
        constructs,
        ruleset_version: rs.version,
      });
    }
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

  // ------------------------------------------------------- local overrides

  /** @internal The ruleset held now, for local overrides. */
  heldRuleset(): Ruleset | undefined {
    return this.held?.rs;
  }

  // A protected ruleset never serves a local override.
  private localOverride(key: string): { readonly type: FlagType; readonly value: JsonValue } | undefined {
    if (this.localOverrides.size === 0 || this.held?.rs.protected) return undefined;
    return this.localOverrides.get(key);
  }

  // Drops every local override when rs belongs to a protected environment.
  private refuseLocalOverrides(rs: Ruleset): string[] {
    if (!rs.protected || this.localOverrides.size === 0) return [];
    const keys = [...this.localOverrides.keys()].sort();
    this.localOverrides = new Map();
    for (const k of keys) {
      this.log("warn", "override_refused", { flag: k, cause: "protected_environment", environment: rs.document.environment?.key });
    }
    return keys;
  }

  // ------------------------------------------------------------- logging

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

/** Thrown by `overrides.set` on a client built without the `allowOverrides` option. */
export class LocalOverridesNotAllowed extends Error {
  constructor() {
    super("flagnetic: local overrides need the allowOverrides option");
    this.name = "LocalOverridesNotAllowed";
  }
}

/** Thrown by `overrides.set` while the held ruleset belongs to a protected environment. */
export class LocalOverrideRefused extends Error {
  constructor(key: string) {
    super(`flagnetic: local override of ${JSON.stringify(key)} refused in a protected environment`);
    this.name = "LocalOverrideRefused";
  }
}

/**
 * Local overrides: substitutions of flag values inside this process that
 * never reach the server. An overridden flag reads reason `local_override`,
 * with no experiment block and the stage unchanged. They exist only with the
 * `allowOverrides` option and are refused in a protected environment.
 */
export class LocalOverrides {
  /** @internal */
  constructor(private readonly c: FlagneticServer) {}

  /**
   * Substitutes value for the flag key and fires the change listeners.
   * Throws LocalOverridesNotAllowed without the `allowOverrides` option and
   * LocalOverrideRefused when the held ruleset's environment is protected,
   * logging `override_refused` either way. The value must be of the flag's
   * type; a key the ruleset lacks takes the type of the value. Throws a
   * TypeError for a value of another type.
   */
  set(key: string, value: JsonValue): void {
    const c = this.c;
    if (!c.allowOverrides) {
      c.log("warn", "override_refused", { flag: key, cause: "not_allowed" });
      throw new LocalOverridesNotAllowed();
    }
    const rs = c.heldRuleset();
    if (rs?.protected) {
      c.log("warn", "override_refused", { flag: key, cause: "protected_environment", environment: rs.document.environment?.key });
      throw new LocalOverrideRefused(key);
    }
    const flagType = rs?.flag(key)?.type;
    const type = (flagType ?? flagTypeOf(value)) as FlagType;
    if (!fitsFlagType(type, value)) {
      throw new TypeError(`flagnetic: a local override of ${JSON.stringify(key)} must be a ${type} value`);
    }
    const next = new Map(c.localOverrides);
    next.set(key, { type, value: type === "json" ? frozenJson(value) : value });
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

/** The flag type a value belongs to: boolean, string, number, anything else JSON. */
export { flagTypeOf as typeOfValue } from "../internal/detail.js";

function frozenJson(v: JsonValue): JsonValue {
  const copy = JSON.parse(JSON.stringify(v)) as JsonValue;
  const freeze = (x: unknown): void => {
    if (typeof x === "object" && x !== null) {
      Object.freeze(x);
      Object.values(x).forEach(freeze);
    }
  };
  freeze(copy);
  return copy;
}

/** A promise resolving after ms of real time; it never keeps a Node process alive. */
function deadline(ms: number): Promise<void> {
  return new Promise<void>((resolve) => {
    const t = setTimeout(resolve, Math.max(0, ms));
    (t as { unref?: () => void }).unref?.();
  });
}

function isObject(v: unknown): v is Context {
  return typeof v === "object" && v !== null;
}

function experimentOf(e: NonNullable<Result["experiment"]>): Experiment {
  return {
    key: e.key,
    variation: e.variation,
    unitType: e.unit_type,
    unitId: e.unit_id,
    inclusionSalt: e.inclusion_salt,
    variationSalt: e.variation_salt,
    inclusionBucket: e.inclusion_bucket,
    variationBucket: e.variation_bucket,
    override: e.override,
  };
}

function union(a: readonly string[], b: readonly string[]): string[] {
  return [...new Set([...a, ...b])].sort();
}
