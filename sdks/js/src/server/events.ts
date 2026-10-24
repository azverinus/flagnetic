// The in-memory queue of exposures and aliases and its sender. An event is recorded with the event ID it
// keeps for every retry, so the platform drops a resend as a duplicate; the
// queue flushes on its interval or at its batch size, is bounded and drops
// its oldest events when it overflows.
import { Backoff } from "../internal/backoff.js";
import { Repeater, type Clock } from "../internal/clock.js";
import type { QueuedEvent } from "../internal/events.js";
import type { Ingest, SendResult } from "./ingest.js";
import type { LogEvent, LogLevel } from "./types.js";

export interface EventQueueConfig {
  /** The most events the queue holds before dropping its oldest. */
  readonly capacity: number;
  /** The most events one batch carries, and the size that flushes at once. */
  readonly batchSize: number;
  /** Milliseconds between flushes. */
  readonly flushInterval: number;
  readonly retryInitial: number;
  readonly retryMax: number;
  /** The retry cap after 401 or 403. */
  readonly unauthorizedMax: number;
  readonly clock: Clock;
  readonly random: () => number;
}

type Log = (level: LogLevel, event: LogEvent, fields: Record<string, unknown>) => void;

/**
 * The queue of exposures and aliases. `record` appends; `flush` sends what
 * is queued; a batch that failed for a reason worth retrying keeps its
 * event IDs and goes again after exponential backoff with full jitter.
 */
export class EventQueue {
  private queue: QueuedEvent[] = [];
  private sending = false;
  private readonly interval: Repeater;
  private retryTimer: unknown;
  /** Set by close: no more events are taken and no retry is scheduled. */
  private closed = false;
  /** Set once close's last flush is over: nothing more is sent at all. */
  private done = false;
  private settled: Promise<void> = Promise.resolve();
  private readonly backoff: Backoff;

  constructor(
    private readonly config: EventQueueConfig,
    private readonly ingest: Ingest,
    private readonly log: Log,
    private readonly trace?: () => void,
  ) {
    this.backoff = new Backoff(config.retryInitial, config.random);
    this.interval = new Repeater(config.clock, config.flushInterval, () => this.flush());
  }

  /** How many events wait to be sent. */
  get queued(): number {
    return this.queue.length;
  }

  /** Starts the flush interval. */
  start(): void {
    this.interval.start();
  }

  /** Appends an event, dropping the oldest when the queue is full. */
  record(event: QueuedEvent): void {
    if (this.closed) return;
    this.queue.push(event);
    const over = this.queue.length - this.config.capacity;
    if (over > 0) {
      this.queue.splice(0, over);
      this.log("warn", "queue_dropped", { dropped: over, capacity: this.config.capacity, cause: "queue_full" });
    }
    if (this.queue.length >= this.config.batchSize) void this.flush();
  }

  /**
   * Sends what is queued and resolves once the batches in flight have
   * settled. A batch waiting out its backoff is left to it, so a flush
   * during one resolves at once; `force` sends it now, which is what close
   * does.
   */
  flush(options: { force?: boolean } = {}): Promise<void> {
    if (this.queue.length === 0) return this.settled;
    if (options.force === true) {
      this.config.clock.clearTimeout(this.retryTimer);
      this.retryTimer = undefined;
    } else if (this.retryTimer !== undefined) {
      return this.settled;
    }
    if (this.sending) return this.settled;
    this.settled = this.send();
    return this.settled;
  }

  /**
   * Stops the timers and sends what is queued one last time: batch after
   * batch while the platform takes them, and no further once one fails,
   * since the caller bounds how long close may take.
   */
  async close(): Promise<void> {
    this.interval.stop();
    this.closed = true;
    await this.flush({ force: true });
    this.done = true;
    this.config.clock.clearTimeout(this.retryTimer);
    this.retryTimer = undefined;
  }

  private async send(): Promise<void> {
    this.sending = true;
    // At most `batchSize` events, and only as many of them as the
    // endpoint's 1 MiB body holds.
    const batch = this.ingest.pack(this.queue.slice(0, this.config.batchSize))[0] ?? [];
    let res: SendResult;
    try {
      res = await this.ingest.post(batch);
    } finally {
      this.sending = false;
      this.trace?.();
    }

    if (res.outcome === "accepted" || res.outcome === "dropped") {
      // Rejected events go with the accepted ones: the platform will never
      // take them.
      const sent = new Set(batch.map((e) => e.event_id));
      this.queue = this.queue.filter((e) => !sent.has(e.event_id));
      this.backoff.reset();
      if (res.outcome === "dropped") {
        this.log("warn", "queue_dropped", { cause: "client_error", status: res.status, events: batch.length });
      } else if (res.result && res.result.rejected.length > 0) {
        this.log("warn", "queue_dropped", {
          cause: "refused_by_the_platform",
          rejected: res.result.rejected.map((r) => r.code),
          accepted: res.result.accepted,
          duplicates: res.result.duplicates,
        });
      }
      if (this.queue.length > 0 && !this.done) await this.flush({ force: true });
      return;
    }

    const cap = res.outcome === "unauthorized" ? this.config.unauthorizedMax : this.config.retryMax;
    // The queue backs off on its own rather than honouring `Retry-After`: a
    // rate limit whose header names minutes would otherwise hold
    // exposures back for minutes, and the protocol allows either.
    const delay = this.backoff.next(cap);
    this.log("warn", "events_failed", {
      cause: res.cause ?? "",
      ...(res.status !== undefined && { status: res.status }),
      events: batch.length,
      retry_in_ms: delay,
    });
    if (this.closed) return;
    this.retryTimer = this.config.clock.setTimeout(() => {
      this.retryTimer = undefined;
      void this.flush();
    }, delay);
  }
}
