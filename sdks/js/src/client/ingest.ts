// The persisted event queue and the batches it posts to /sdk/v1/events.
// Exposures and aliases are appended with the event ID they keep for every
// retry. The evaluation summary is materialized on its own interval, and
// when the page is hidden or closed: the queue never holds more than one
// unsent window of it, merged forward through an outage and dropped once its
// interval began over an hour ago, so summaries cannot push exposures out of
// a full queue. A batch goes out when the queue holds an exposure or an
// alias, and carries whatever else is queued. The queue
// lives in browser storage under the SDK key, so a tab closed mid-flush
// sends its events on the next load, and a hidden or closing tab hands it
// to the page's beacon.
import { Backoff } from "../internal/backoff.js";
import type { Clock } from "../internal/clock.js";
import { countKey, orderRows, packBatch, summaryEvents, SummaryCounter, utf8Length, type QueuedEvent, type SummaryRow } from "../internal/events.js";
import { causeOf, type LogLevel } from "../internal/logger.js";
import type { SafeStore } from "./storage.js";
import type { LogEvent, Reason } from "./types.js";
import { SCHEMA_VERSION_MAX, SDK_API_PATH } from "../version.js";

export { SummaryCounter, type QueuedEvent };

/** One row of an evaluation summary: no unit IDs, no values. */
export type SummaryCount = SummaryRow<Reason>;

export interface IngestConfig {
  readonly baseUrl: string;
  readonly sdkKey: string;
  readonly sdkName: string;
  readonly sdkVersion: string;
  readonly platform: string;
  readonly appVersion?: string;
  /** The most events the queue holds before dropping its oldest. */
  readonly capacity: number;
  /**
   * The store key of this SDK key's queue. Tabs of one origin with the same
   * key share it, so every change reads it first and writes back what it
   * read with only its own change applied: another tab's unsent events
   * survive, and a closed tab's leftovers go with the next batch.
   */
  readonly queueKey: string;
  /** The queue length at which a recorded event flushes at once, and the most events one beacon carries. */
  readonly batchSize: number;
  /** Milliseconds between flushes while the tab is visible. */
  readonly flushInterval: number;
  /** Milliseconds between evaluation summaries while the tab is visible. */
  readonly summaryInterval: number;
  /** The age past which an unsent summary window is dropped, in milliseconds. */
  readonly maxWindowAge: number;
  readonly requestTimeout: number;
  readonly retryInitial: number;
  readonly retryMax: number;
  readonly unauthorizedMax: number;
  readonly sendSummaries: boolean;
  readonly clock: Clock;
  /** The wall clock, for timestamps and event IDs. */
  readonly now: () => number;
  readonly random: () => number;
  readonly fetch: typeof fetch;
  /** The page's `navigator.sendBeacon` with a string body; undefined where there is none. */
  readonly beacon: ((url: string, body: string) => boolean) | undefined;
  /** The ruleset version the held values were evaluated against. */
  readonly rulesetVersion: () => number | undefined;
}

type Log = (level: LogLevel, event: LogEvent, fields: Record<string, unknown>) => void;

/**
 * The bytes a page may have in beacons and `keepalive` requests not yet
 * sent: browsers refuse a beacon beyond 64 KiB in flight.
 */
const MAX_BEACON_BYTES = 64 * 1024;

/**
 * The event queue and its sender. `record` appends, `flush` sends what is
 * queued, and a batch that failed for a reason worth retrying is kept with
 * its event IDs and retried with exponential backoff and full jitter.
 */
export class Ingest {
  /** The event IDs of the batch this tab is posting, which a beacon leaves to it. */
  private inFlight = new Set<string>();
  private timer: unknown;
  private summaryTimer: unknown;
  private retryTimer: unknown;
  private closed = false;
  private visible = true;
  private settled: Promise<void> = Promise.resolve();
  private readonly backoff: Backoff;

  constructor(
    private readonly config: IngestConfig,
    /** The counter the client fills on every evaluation; its rows ride along in each batch. */
    private readonly summary: SummaryCounter<Reason>,
    private readonly store: SafeStore,
    private readonly log: Log,
    private readonly trace?: (event: "events_handled") => void,
  ) {
    this.backoff = new Backoff(config.retryInitial, config.random);
  }

  /** How many events wait to be sent, from every tab sharing the key. */
  get queued(): number {
    return this.read().length;
  }

  /** Starts the flush interval; called once the client is running. */
  start(): void {
    this.arm();
    this.armSummary();
  }

  /**
   * Follows the tab: the flush interval runs while it is visible and waits
   * while it is hidden, where what is queued stays in storage.
   */
  setVisible(visible: boolean): void {
    if (visible === this.visible) return;
    this.visible = visible;
    if (visible) {
      this.arm();
      this.armSummary();
    } else {
      this.config.clock.clearTimeout(this.timer);
      this.config.clock.clearTimeout(this.summaryTimer);
      this.timer = undefined;
      this.summaryTimer = undefined;
    }
  }

  /** Appends an event, dropping the oldest when the queue is full. */
  record(event: QueuedEvent): void {
    if (this.append([event]) >= this.config.batchSize) void this.drain();
  }

  /**
   * Sends what is queued, the evaluation summary included, and resolves once
   * the batches in flight have settled. A batch kept for a retry is left to
   * its backoff.
   */
  async flush(): Promise<void> {
    this.materializeSummary();
    return this.drain();
  }

  // Sends what is queued, without turning the counts into a summary first.
  private async drain(): Promise<void> {
    if (this.closed || this.read().length === 0) return;
    // A retry is already scheduled: let its backoff run rather than hammering.
    if (this.retryTimer !== undefined) return this.settled;
    if (this.inFlight.size > 0) return this.settled;
    this.settled = this.send();
    return this.settled;
  }

  /**
   * Hands the queue, the evaluation summary included, to the page's beacon,
   * which the browser posts even after the page has gone: called on every
   * hide, not only the first, since a page hidden and then closed records
   * events in between. The beacon cannot set a header, so the client key
   * travels as `sdk_key` and the envelope as `text/plain`, which make it a
   * request without a CORS preflight. Batches go until the page's beacon
   * quota is spent and leave the queue once the browser took them: nobody
   * reads the answer, and a retry would only be dropped as a duplicate.
   * What the quota leaves stays queued for the next load; a batch in flight
   * is left to its own request. Where there is no beacon, or the browser
   * takes none, the queue is flushed by a request, which may still land.
   */
  beacon(): void {
    if (this.closed) return;
    this.materializeSummary();
    const beacon = this.config.beacon;
    const url = `${this.config.baseUrl}${SDK_API_PATH}/events?sdk_key=${encodeURIComponent(this.config.sdkKey)}`;
    let budget = MAX_BEACON_BYTES;
    for (let first = true; ; first = false) {
      const pending = this.read().filter((e) => !this.inFlight.has(e.event_id));
      if (pending.length === 0) return;
      let size = utf8Length(this.envelope([]));
      const batch: QueuedEvent[] = [];
      for (const e of pending) {
        // One comma per event after the first.
        const cost = utf8Length(JSON.stringify(e)) + (batch.length > 0 ? 1 : 0);
        if (batch.length === this.config.batchSize || size + cost > budget) break;
        batch.push(e);
        size += cost;
      }
      if (batch.length === 0 || !beacon?.(url, this.envelope(batch))) {
        if (first) void this.drain();
        else this.log("warn", "events_failed", { cause: "beacon_refused", events: pending.length });
        return;
      }
      budget -= size;
      this.remove(batch);
    }
  }

  /**
   * Forgets what is waiting to be sent: the stored queue, every tab's, and
   * the evaluation counts not yet turned into a summary, so nothing recorded
   * before the call is written back or sent. A pending retry is cancelled; a
   * batch in flight finishes, and its answer only removes what is no longer
   * stored.
   */
  clear(): void {
    this.summary.take();
    this.store.remove(this.config.queueKey);
    this.config.clock.clearTimeout(this.retryTimer);
    this.retryTimer = undefined;
    this.backoff.reset();
  }

  /** Stops the timers; what is queued stays in storage for the next load. */
  close(): void {
    this.closed = true;
    this.config.clock.clearTimeout(this.timer);
    this.config.clock.clearTimeout(this.summaryTimer);
    this.config.clock.clearTimeout(this.retryTimer);
  }

  // ----------------------------------------------------------------- queue

  private read(): QueuedEvent[] {
    const raw = this.store.get(this.config.queueKey);
    if (raw === null) return [];
    try {
      const parsed: unknown = JSON.parse(raw);
      if (!Array.isArray(parsed)) throw new TypeError("not an array");
      return (parsed as unknown[]).filter(isQueuedEvent);
    } catch (e) {
      // A torn or foreign entry is not worth keeping.
      this.log("warn", "store_failed", { operation: "read", key: this.config.queueKey, cause: causeOf(e) });
      this.store.remove(this.config.queueKey);
      return [];
    }
  }

  private write(queue: readonly QueuedEvent[]): void {
    if (queue.length === 0) this.store.remove(this.config.queueKey);
    else this.store.set(this.config.queueKey, JSON.stringify(queue));
  }

  /** Appends events to the stored queue, dropping beyond capacity; returns its length. */
  private append(events: readonly QueuedEvent[]): number {
    return this.replace([...this.read(), ...events]);
  }

  // Stores a queue, dropping what does not fit: evaluation summaries first,
  // oldest first, because counts nobody is waiting for must not push out an
  // exposure or an alias, which an experiment's analysis cannot do without.
  private replace(queue: QueuedEvent[]): number {
    let over = queue.length - this.config.capacity;
    if (over > 0) {
      this.log("warn", "queue_dropped", { dropped: over, capacity: this.config.capacity });
      const kept = queue.filter((e) => {
        if (over > 0 && e.type === "evaluation_summary") {
          over--;
          return false;
        }
        return true;
      });
      queue = over > 0 ? kept.slice(over) : kept;
    }
    this.write(queue);
    return queue.length;
  }

  /** Removes the events of a batch from the stored queue, and nothing another tab added since. */
  private remove(batch: readonly QueuedEvent[]): number {
    const sent = new Set(batch.map((e) => e.event_id));
    const queue = this.read().filter((e) => !sent.has(e.event_id));
    this.write(queue);
    return queue.length;
  }

  // Turns the counts collected since the last summary into the queue's one
  // summary window, merging in the one still unsent unless its interval
  // began over an hour ago. A window in flight is left to its request. The
  // merged window gets new event IDs, as a merged window does in server mode:
  // the events it replaces were never accepted.
  //
  // On the summary interval the counts wait in memory while a window is in
  // flight, so the queue never holds two unsent windows; a flush, a hide and
  // a close cannot wait, and leave the window in flight to its request.
  private materializeSummary(waitForFlight = false): void {
    if (!this.config.sendSummaries) return;
    const queue = this.read();
    if (waitForFlight && queue.some((e) => e.type === "evaluation_summary" && this.inFlight.has(e.event_id))) return;
    const counted = this.summary.size > 0 ? this.summary.take() : undefined;
    const kept = queue.filter((e) => e.type === "evaluation_summary" && !this.inFlight.has(e.event_id));
    if (!counted && kept.length === 0) return;
    const now = this.config.now();
    const end = counted?.intervalEnd ?? now;
    let start = counted?.intervalStart ?? end;
    const byKey = new Map<string, SummaryRow<Reason>>();
    const add = (row: SummaryRow<Reason>): void => {
      const id = countKey(row.flag, row.variation, row.reason);
      const seen = byKey.get(id);
      byKey.set(id, seen ? { ...seen, count: seen.count + row.count } : row);
    };
    for (const row of counted?.counts ?? []) add(row);
    if (kept.length > 0) {
      const began = Math.min(...kept.map((e) => Date.parse(String(e.interval_start))));
      const rows = kept.flatMap((e) => (Array.isArray(e.counts) ? (e.counts as SummaryRow<Reason>[]) : []));
      if (end - began > this.config.maxWindowAge) {
        this.log("warn", "queue_dropped", { kind: "evaluation_summary", cause: "window_older_than_one_hour", rows: rows.length, age_ms: end - began });
      } else {
        if (began < start) start = began;
        rows.forEach(add);
      }
    }
    const replaced = new Set(kept.map((e) => e.event_id));
    const rest = queue.filter((e) => !replaced.has(e.event_id));
    const merged =
      byKey.size === 0
        ? []
        : summaryEvents(
            now,
            this.config.random,
            {
              intervalStart: start,
              intervalEnd: end,
              rulesetVersion: this.config.rulesetVersion() ?? 0,
              ...(this.config.appVersion !== undefined && { appVersion: this.config.appVersion }),
            },
            orderRows([...byKey.values()]),
          );
    this.replace([...rest, ...merged]);
  }

  // ------------------------------------------------------------------ send

  private arm(): void {
    this.config.clock.clearTimeout(this.timer);
    if (this.closed || !this.visible) return;
    this.timer = this.config.clock.setTimeout(() => {
      this.timer = undefined;
      // The flush interval carries exposures and aliases: a queue of nothing
      // but a summary waits for the summary interval, so an app that reads a
      // flag every second does not post every few seconds.
      const due = this.read().some((e) => e.type !== "evaluation_summary");
      void (due ? this.drain() : Promise.resolve()).finally(() => { this.arm(); });
    }, this.config.flushInterval);
  }

  private armSummary(): void {
    this.config.clock.clearTimeout(this.summaryTimer);
    if (this.closed || !this.visible) return;
    this.summaryTimer = this.config.clock.setTimeout(() => {
      this.summaryTimer = undefined;
      this.materializeSummary(true);
      void this.drain().finally(() => { this.armSummary(); });
    }, this.config.summaryInterval);
  }

  private async send(): Promise<void> {
    const batch = packBatch(this.read(), utf8Length(this.envelope([])));
    this.inFlight = new Set(batch.map((e) => e.event_id));
    let outcome: "sent" | "retry" | "drop" = "retry";
    let status: number | undefined;
    let cause = "";
    try {
      const res = await this.post(batch);
      status = res.status;
      if (res.status === 202) {
        outcome = "sent";
      } else if (res.status === 429 || res.status >= 500 || res.status === 401 || res.status === 403) {
        // Safe to retry: every event carries its own ID. The backoff alone
        // spaces the retries, as in server mode: a `Retry-After` of a minute
        // would hold back the exposures of a short visit until it is over.
        outcome = "retry";
        cause = `status ${res.status}`;
      } else {
        // Any other 4xx: this request will never be accepted.
        outcome = "drop";
        cause = `status ${res.status}`;
      }
    } catch (e) {
      cause = `network error: ${causeOf(e)}`;
    } finally {
      this.inFlight = new Set();
      this.trace?.("events_handled");
    }

    if (outcome === "sent" || outcome === "drop") {
      // Rejected events are dropped with the accepted ones: the server will
      // never take them.
      const left = this.remove(batch);
      this.backoff.reset();
      if (outcome === "drop") this.log("warn", "events_failed", { cause, status, events: batch.length, dropped: batch.length });
      if (left > 0 && !this.closed) await this.drain();
      return;
    }
    const cap = status === 401 || status === 403 ? this.config.unauthorizedMax : this.config.retryMax;
    const delay = this.backoff.next(cap);
    this.log("warn", "events_failed", { cause, ...(status !== undefined && { status }), events: batch.length, retry_in_ms: delay });
    if (this.closed) return;
    this.retryTimer = this.config.clock.setTimeout(() => {
      this.retryTimer = undefined;
      void this.drain();
    }, delay);
  }

  private envelope(events: readonly QueuedEvent[]): string {
    return JSON.stringify({
      schema_version: SCHEMA_VERSION_MAX,
      sdk: { name: this.config.sdkName, version: this.config.sdkVersion, platform: this.config.platform },
      sent_at: new Date(this.config.now()).toISOString(),
      events,
    });
  }

  private async post(events: readonly QueuedEvent[]): Promise<Response> {
    const body = this.envelope(events);
    const controller = new AbortController();
    const timer = this.config.clock.setTimeout(() => { controller.abort(new Error(`timed out after ${this.config.requestTimeout} ms`)); }, this.config.requestTimeout);
    try {
      return await this.config.fetch(`${this.config.baseUrl}${SDK_API_PATH}/events`, {
        method: "POST",
        // Only headers the server's CORS preflight allows; a browser cannot
        // add the User-Agent this SDK would like to send, so the envelope's
        // `sdk` block carries its identity instead.
        headers: {
          Authorization: `Bearer ${this.config.sdkKey}`,
          "Content-Type": "application/json",
          "Flagnetic-Schema-Version": String(SCHEMA_VERSION_MAX),
        },
        body,
        signal: controller.signal,
      });
    } finally {
      this.config.clock.clearTimeout(timer);
    }
  }
}

function isQueuedEvent(v: unknown): v is QueuedEvent {
  if (typeof v !== "object" || v === null) return false;
  const e = v as Record<string, unknown>;
  return typeof e.type === "string" && typeof e.event_id === "string";
}

