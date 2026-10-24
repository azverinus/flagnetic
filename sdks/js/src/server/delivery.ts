// Delivery of the ruleset over the SDK API: the fetch with ETag
// revalidation, the ruleset stream and the safety poll. It decides when to fetch and what a response
// means; the client owns the held ruleset and the stage.
import { decodeRuleset, type Ruleset } from "../evaluator/evaluate.js";
import { Backoff } from "../internal/backoff.js";
import type { Clock } from "../internal/clock.js";
import { retryAfterMs, trimTrailingSlashes } from "../internal/http.js";
import { causeOf } from "./logger.js";
import { SseParser } from "../internal/sse.js";
import type { LogEvent, LogLevel } from "./types.js";
import { SCHEMA_VERSION_MAX, SCHEMA_VERSION_MIN, SDK_API_PATH, SUPPORTED_SCHEMA_VERSIONS, VERSION } from "../version.js";

/** Every interval of delivery, in milliseconds. */
export interface Timing {
  readonly requestTimeout: number;
  /** Safety poll while the stream is connected. */
  readonly pollConnected: number;
  /** Safety poll while the stream is down or disabled. */
  readonly pollDisconnected: number;
  readonly retryInitial: number;
  readonly retryMax: number;
  /** The retry cap after 401 or 403. */
  readonly unauthorizedMax: number;
  readonly streamRetryInitial: number;
  readonly streamRetryMax: number;
  /** A connection this long resets the stream backoff. */
  readonly streamStable: number;
  /** Silence after which the stream reconnects. */
  readonly streamSilence: number;
  /** The jitter before reconnecting after a server-side close. */
  readonly streamCloseJitter: number;
  /** Retries of a fetch that answered below a streamed version. */
  readonly catchUp: readonly number[];
  /** How long the first fetch waits for the stream to connect. */
  readonly streamFirstWait: number;
}

export const defaultTiming: Timing = {
  requestTimeout: 10_000,
  pollConnected: 300_000,
  pollDisconnected: 30_000,
  retryInitial: 1_000,
  retryMax: 300_000,
  unauthorizedMax: 900_000,
  streamRetryInitial: 1_000,
  streamRetryMax: 30_000,
  streamStable: 60_000,
  streamSilence: 45_000,
  streamCloseJitter: 2_000,
  catchUp: [1_000, 2_000, 4_000],
  streamFirstWait: 2_000,
};

/** What delivery tells the client. */
export interface DeliveryHost {
  /** The version of the held ruleset, if any. */
  heldVersion(): number | undefined;
  /** A 200 was accepted. */
  accept(rs: Ruleset, document: string): void;
  /** A 304: the held ruleset is current. */
  notModified(): void;
  /** An attempt failed; the held ruleset stays. */
  failed(): void;
  log(level: LogLevel, event: LogEvent, fields: Record<string, unknown>): void;
}

export { realClock, type Clock } from "../internal/clock.js";

export interface DeliveryConfig {
  readonly sdkKey: string;
  readonly baseUrl: string;
  readonly platform: string;
  readonly sse: boolean;
  readonly timing: Timing;
  readonly random: () => number;
  readonly clock: Clock;
  readonly fetch: typeof fetch;
  /** Told when delivery has handled a response, a stream chunk or a stream's end: a test seam. */
  readonly trace?: (event: TraceEvent) => void;
}

export type TraceEvent =
  | { readonly kind: "fetch_handled" }
  | { readonly kind: "events_handled" }
  | { readonly kind: "stream_opened" }
  | { readonly kind: "stream_ended" }
  | { readonly kind: "stream_read"; readonly bytes: number };

type Outcome =
  | { readonly kind: "accepted"; readonly rs: Ruleset; readonly text: string }
  | { readonly kind: "not_modified" }
  | { readonly kind: "failed"; readonly failure: AttemptFailed };

/** Why an attempt (a fetch, a stream connection) failed. */
class AttemptFailed extends Error {
  constructor(
    message: string,
    readonly status?: number,
    readonly retryAfter?: number,
    /** The answer was a version below the one held: a replica behind. */
    readonly behind = false,
  ) {
    super(message);
  }
}

const unauthorized = (status: number): boolean => status === 401 || status === 403;

/** The longest delay a timer holds (2^31 - 1 ms); a longer one fires at once. */
const MAX_TIMER_DELAY = 2_147_483_647;

export class Delivery {
  private closed = false;
  private readonly userAgent: string;
  private readonly base: string;

  // The fetch loop.
  private fetching = false;
  private fetchStarted = false;
  private fetchAgain = false;
  private fetchTimer: unknown;
  private fetchAbort: AbortController | undefined;
  private readonly fetchBackoff: Backoff;
  /** The highest version the stream announced. */
  private announced = 0;
  private catchUpStep = 0;
  private fetchDueAt = 0;
  /** Whether the fetch loop is backing off after a failure. */
  private retrying = false;

  // Once-per-episode logs. A key the fetch saw refused reopens the stream
  // when it works again; a stream refused on its own stays closed.
  private revoked = false;
  private fetchRefused = false;
  private schemaLogged = false;

  // The stream.
  private streamAbort: AbortController | undefined;
  private streamReader: ReadableStreamDefaultReader<Uint8Array> | undefined;
  /** Counts stream runs, so an old run unwinding never touches a newer one's state. */
  private streamRun = 0;
  private streamTimer: unknown;
  private silenceTimer: unknown;
  private streamUp = false;
  private streamStopped = false;
  private readonly streamBackoff: Backoff;
  private readonly clock: Clock;

  constructor(
    private readonly config: DeliveryConfig,
    private readonly host: DeliveryHost,
  ) {
    this.userAgent = `flagnetic-js/${VERSION} (${config.platform})`;
    this.base = trimTrailingSlashes(config.baseUrl);
    this.fetchBackoff = new Backoff(config.timing.retryInitial, config.random);
    this.streamBackoff = new Backoff(config.timing.streamRetryInitial, config.random);
    this.clock = config.clock;
  }

  /** The headers every request carries. */
  private identity(): Record<string, string> {
    return { Authorization: `Bearer ${this.config.sdkKey}`, "User-Agent": this.userAgent };
  }

  private trace(event: TraceEvent): void {
    this.config.trace?.(event);
  }

  /**
   * Opens the stream first and then fetches, so that no change falls
   * between the two: the first fetch waits for the stream to connect, for
   * at most the stream's first wait.
   */
  async start(): Promise<void> {
    if (this.config.sse) {
      const settled = this.openStream();
      let timer: unknown;
      const waited = new Promise<void>((resolve) => (timer = this.clock.setTimeout(resolve, this.config.timing.streamFirstWait)));
      await Promise.race([settled, waited]);
      this.clock.clearTimeout(timer);
    }
    if (!this.fetchStarted) void this.fetch();
  }

  close(): void {
    this.closed = true;
    this.clock.clearTimeout(this.fetchTimer);
    this.clock.clearTimeout(this.streamTimer);
    this.clock.clearTimeout(this.silenceTimer);
    this.fetchAbort?.abort();
    this.endStream();
  }

  // ---------------------------------------------------------------- fetch

  private async fetch(): Promise<void> {
    if (this.closed) return;
    if (this.fetching) {
      this.fetchAgain = true;
      return;
    }
    this.clock.clearTimeout(this.fetchTimer);
    this.fetching = true;
    this.fetchStarted = true;
    try {
      await this.fetchOnce();
    } catch (e) {
      // Only the caller's code (a listener, a logger) could throw here, and
      // both are guarded; still, the loop must never stop.
      this.fetching = false;
      this.host.log("error", "fetch_failed", { cause: causeOf(e) });
      this.schedule(this.pollInterval());
    } finally {
      this.trace({ kind: "fetch_handled" });
    }
  }

  private async fetchOnce(): Promise<void> {
    const announcedAtStart = this.announced;
    const outcome = await this.request();
    this.fetching = false;
    if (this.closed) return;
    const again = this.fetchAgain;
    this.fetchAgain = false;

    if (outcome.kind === "failed") {
      const f = outcome.failure;
      if (f.behind && this.catchUpPending()) {
        this.host.log("warn", "fetch_failed", { cause: f.message, retry_in_ms: this.catchUpDelay() });
        this.host.failed();
        this.catchUp();
        return;
      }
      const cap = f.status !== undefined && unauthorized(f.status) ? this.config.timing.unauthorizedMax : this.config.timing.retryMax;
      // A far-off `Retry-After` is clamped to the longest delay a timer
      // holds: beyond it the timer overflows and fires at once.
      const delay = Math.min(Math.max(this.fetchBackoff.next(cap), f.retryAfter ?? 0), MAX_TIMER_DELAY);
      this.host.log("warn", "fetch_failed", {
        cause: f.message,
        ...(f.status !== undefined && { status: f.status }),
        retry_in_ms: delay,
      });
      this.host.failed();
      // A failed fetch is retried by backoff alone; the poll waits.
      this.retrying = true;
      this.schedule(delay);
      return;
    }
    this.retrying = false;
    this.fetchBackoff.reset();
    this.revoked = false;
    if (this.fetchRefused) {
      // The key works again: the stream comes back, if it was ever wanted.
      this.fetchRefused = false;
      if (this.streamStopped && this.config.sse) {
        this.streamStopped = false;
        void this.openStream();
      }
    }
    if (outcome.kind === "accepted") this.host.accept(outcome.rs, outcome.text);
    else this.host.notModified();

    const held = this.host.heldVersion() ?? 0;
    if (held >= this.announced) {
      this.catchUpStep = 0;
      this.schedule(this.pollInterval());
    } else if (again || this.announced > announcedAtStart) {
      // A newer version was announced while this fetch was in flight.
      this.catchUpStep = 0;
      void this.fetch();
    } else if (!this.catchUp()) {
      this.schedule(this.pollInterval());
    }
  }

  // A replica behind the announced version: is a retry of the 1, 2, 4 s
  // sequence left?
  private catchUpPending(): boolean {
    return (this.host.heldVersion() ?? 0) < this.announced && this.catchUpStep < this.config.timing.catchUp.length;
  }

  private catchUpDelay(): number {
    return this.config.timing.catchUp[this.catchUpStep] ?? 0;
  }

  // catchUp schedules the next retry of the sequence and reports whether it
  // did. Once the sequence is spent the announcement is dropped, so the
  // polls that follow do not start it again; the next event would.
  private catchUp(): boolean {
    if (!this.catchUpPending()) {
      this.catchUpStep = 0;
      this.announced = Math.min(this.announced, this.host.heldVersion() ?? 0);
      return false;
    }
    this.schedule(this.catchUpDelay());
    this.catchUpStep++;
    return true;
  }

  private pollInterval(): number {
    return this.streamUp ? this.config.timing.pollConnected : this.config.timing.pollDisconnected;
  }

  private schedule(delay: number): void {
    this.clock.clearTimeout(this.fetchTimer);
    if (this.closed) return;
    this.fetchTimer = this.clock.setTimeout(() => void this.fetch(), delay);
    this.fetchDueAt = this.clock.now() + delay;
  }



  /** One ruleset request and what its response means. */
  private async request(): Promise<Outcome> {
    const held = this.host.heldVersion();
    const abort = new AbortController();
    this.fetchAbort = abort;
    const timer = this.clock.setTimeout(() => {
      abort.abort(new AttemptFailed(`timed out after ${this.config.timing.requestTimeout} ms`));
    }, this.config.timing.requestTimeout);
    try {
      const headers: Record<string, string> = {
        ...this.identity(),
        Accept: "application/json",
        "Accept-Encoding": "gzip",
        "Flagnetic-Schema-Version": String(SCHEMA_VERSION_MAX),
      };
      if (held !== undefined) headers["If-None-Match"] = `"${held}"`;
      let res: Response;
      let text: string;
      try {
        res = await this.config.fetch(`${this.base}${SDK_API_PATH}/ruleset`, { headers, signal: abort.signal });
        if (res.status === 304) {
          await res.body?.cancel();
          return { kind: "not_modified" };
        }
        if (unauthorized(res.status)) {
          await res.body?.cancel();
          this.keyRevoked(res.status);
          this.fetchRefused = true;
          this.stopStream();
          throw new AttemptFailed(`status ${res.status}`, res.status);
        }
        if (res.status !== 200) {
          await res.body?.cancel();
          throw new AttemptFailed(`status ${res.status}`, res.status, retryAfterMs(res.headers.get("Retry-After"), this.clock.now()));
        }
        text = await res.text();
      } catch (e) {
        if (e instanceof AttemptFailed) throw e;
        const reason: unknown = abort.signal.aborted ? abort.signal.reason : e;
        throw new AttemptFailed(reason instanceof AttemptFailed ? reason.message : `network error: ${causeOf(e)}`);
      }
      return { kind: "accepted", rs: this.parse(text, held), text };
    } catch (e) {
      return { kind: "failed", failure: e instanceof AttemptFailed ? e : new AttemptFailed(causeOf(e)) };
    } finally {
      this.clock.clearTimeout(timer);
      if (this.fetchAbort === abort) this.fetchAbort = undefined;
    }
  }

  /** Decodes a 200 body, refusing what the fallback chain does not accept. */
  private parse(text: string, held: number | undefined): Ruleset {
    let data: unknown;
    try {
      data = JSON.parse(text);
    } catch {
      throw new AttemptFailed(text.trim() === "" ? "parse: empty body" : "parse: malformed JSON");
    }
    let rs: Ruleset;
    try {
      rs = decodeRuleset(data);
    } catch (e) {
      const schema = typeof data === "object" && data !== null ? (data as Record<string, unknown>).schema_version : undefined;
      if (typeof schema === "number" && Number.isInteger(schema) && (schema < SCHEMA_VERSION_MIN || schema > SCHEMA_VERSION_MAX)) {
        if (!this.schemaLogged) {
          this.schemaLogged = true;
          this.host.log("error", "schema_unsupported", { schema_version: schema, supported: SUPPORTED_SCHEMA_VERSIONS });
        }
        throw new AttemptFailed(`schema_version ${schema} unsupported`);
      }
      throw new AttemptFailed(`parse: ${causeOf(e)}`);
    }
    if (held !== undefined && rs.version < held) {
      throw new AttemptFailed(`version ${rs.version} below the held ${held}`, undefined, undefined, true);
    }
    return rs;
  }

  private keyRevoked(status: number): void {
    if (this.revoked) return;
    this.revoked = true;
    this.host.log("error", "key_revoked", { status });
  }

  // ---------------------------------------------------------------- stream

  /** Opens the stream; resolves once it connected or failed to. */
  private openStream(): Promise<void> {
    let settle: () => void = () => undefined;
    const settled = new Promise<void>((resolve) => (settle = resolve));
    void this.runStream(settle);
    return settled;
  }

  private async runStream(settle: () => void): Promise<void> {
    if (this.closed || this.streamStopped) {
      settle();
      return;
    }
    const run = ++this.streamRun;
    const abort = new AbortController();
    this.streamAbort = abort;
    const headers: Record<string, string> = {
      ...this.identity(),
      Accept: "text/event-stream",
      "Cache-Control": "no-cache",
    };
    const held = this.host.heldVersion();
    if (held !== undefined) headers["Last-Event-ID"] = String(held);

    let connectedAt: number | undefined;
    let cause: string;
    let clean = false;
    try {
      this.armSilence(abort);
      const res = await this.config.fetch(`${this.base}${SDK_API_PATH}/stream`, { headers, signal: abort.signal });
      if (unauthorized(res.status)) {
        this.clock.clearTimeout(this.silenceTimer);
        await res.body?.cancel();
        this.keyRevoked(res.status);
        this.stopStream();
        settle();
        this.trace({ kind: "stream_ended" });
        return;
      }
      const type = res.headers.get("Content-Type") ?? "";
      if (res.status !== 200 || !type.startsWith("text/event-stream") || !res.body) {
        await res.body?.cancel();
        throw new Error(`status ${res.status} ${type}`.trim());
      }
      connectedAt = this.clock.now();
      this.streamUp = true;
      this.host.log("info", "sse_connected", {});
      // Every (re)connect is followed by a fetch: versions may have been
      // missed. While fetches back off, the pending retry is that fetch.
      if (!this.retrying) void this.fetch();
      settle();
      this.trace({ kind: "stream_opened" });
      const parser = new SseParser();
      const reader = res.body.getReader();
      this.streamReader = reader;
      for (;;) {
        const { done, value } = await reader.read();
        // A cancelled reader reads as done; it was us, not the server.
        if (abort.signal.aborted) throw abort.signal.reason;
        if (done) break;
        if (this.clock.now() - connectedAt >= this.config.timing.streamStable) {
          this.streamBackoff.reset();
        }
        this.armSilence(abort);
        for (const e of parser.feed(value)) this.onEvent(e.event, e.data);
        this.trace({ kind: "stream_read", bytes: value.byteLength });
      }
      cause = "closed by the server";
      clean = true;
    } catch (e) {
      const reason: unknown = abort.signal.aborted ? abort.signal.reason : e;
      cause = reason instanceof AttemptFailed ? reason.message : causeOf(e);
    }
    if (run !== this.streamRun) {
      // A newer run replaced this one while it unwound.
      settle();
      this.trace({ kind: "stream_ended" });
      return;
    }
    this.clock.clearTimeout(this.silenceTimer);
    this.streamReader = undefined;
    this.streamUp = false;
    if (this.stopped()) {
      settle();
      this.trace({ kind: "stream_ended" });
      return;
    }
    const lasted = connectedAt === undefined ? 0 : this.clock.now() - connectedAt;
    if (lasted >= this.config.timing.streamStable) this.streamBackoff.reset();
    // After a server-side close only jitter spreads the reconnects; after
    // an error the backoff does.
    const delay = clean
      ? Math.floor(this.config.random() * this.config.timing.streamCloseJitter)
      : this.streamBackoff.next(this.config.timing.streamRetryMax);
    this.host.log("warn", "sse_dropped", { cause, retry_in_ms: delay });
    // Polling takes over at the short interval while the stream is down.
    if (!this.fetching && !this.retrying && this.fetchDueAt - this.clock.now() > this.config.timing.pollDisconnected) {
      this.schedule(this.config.timing.pollDisconnected);
    }
    this.streamTimer = this.clock.setTimeout(() => void this.openStream(), delay);
    settle();
    this.trace({ kind: "stream_ended" });
  }

  // A method, so that the check reads the fields afresh after an await.
  private stopped(): boolean {
    return this.closed || this.streamStopped;
  }

  private armSilence(abort: AbortController): void {
    this.clock.clearTimeout(this.silenceTimer);
    const silence = this.config.timing.streamSilence;
    this.silenceTimer = this.clock.setTimeout(() => {
      this.endStream(new AttemptFailed(`no frame for ${silence} ms`), abort);
    }, silence);
  }

  private onEvent(event: string, data: string): void {
    if (event !== "ruleset") return;
    let version: unknown;
    try {
      version = (JSON.parse(data) as { version?: unknown }).version;
    } catch {
      return;
    }
    if (typeof version !== "number" || !Number.isInteger(version)) return;
    if (version <= (this.host.heldVersion() ?? -1) || version <= this.announced) return;
    this.announced = version;
    this.catchUpStep = 0;
    if (!this.retrying) void this.fetch();
  }

  /** Closes the stream for good, until a fetch succeeds again. */
  private stopStream(): void {
    this.streamStopped = true;
    this.streamUp = false;
    this.clock.clearTimeout(this.streamTimer);
    this.endStream();
  }

  // endStream aborts the stream's request and cancels its reader: on Node 18
  // an abort alone does not end a read already waiting on the body.
  private endStream(reason?: unknown, abort = this.streamAbort): void {
    abort?.abort(reason);
    if (abort === this.streamAbort) this.streamReader?.cancel(reason).catch(() => undefined);
  }
}

