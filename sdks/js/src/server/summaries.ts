// The evaluation summary of server mode: counts per flag, variation and
// evaluation reason over a 60 s window, reported with the ruleset version
// held at the window's end. No unit ID, attribute or flag value is in it,
// by construction — the counter only ever sees a flag key, a variation key
// and a reason.
import { Backoff } from "../internal/backoff.js";
import { Repeater, type Clock } from "../internal/clock.js";
import { countKey, orderRows, summaryEvents, SummaryCounter, type QueuedEvent, type SummaryRow } from "../internal/events.js";
import type { Ingest, SendResult } from "./ingest.js";
import type { DetailReason, LogEvent, LogLevel } from "./types.js";

export interface SummariesConfig {
  /** Milliseconds between reports: 60 000 by default. */
  readonly interval: number;
  /**
   * How old the interval of a window whose report failed may be before the
   * window is dropped rather than merged forward: an hour, so a long outage
   * does not eventually deliver counts nobody can place. The interval's
   * start is what ages, because a merged window is measured from where its
   * counting began.
   */
  readonly maxWindowAge: number;
  readonly retryInitial: number;
  readonly retryMax: number;
  /** The retry cap after 401 or 403. */
  readonly unauthorizedMax: number;
  readonly clock: Clock;
  /** The wall clock behind the interval's timestamps and the event IDs. */
  readonly now: () => number;
  readonly random: () => number;
  readonly appVersion?: string;
  /** The version of the held ruleset, read at the interval's end. */
  readonly rulesetVersion: () => number | undefined;
}

type Log = (level: LogLevel, event: LogEvent, fields: Record<string, unknown>) => void;

/** What was evaluated over one interval. */
interface Window {
  start: number;
  end: number;
  rows: SummaryRow<DetailReason>[];
}

export class Summaries {
  private readonly counter: SummaryCounter<DetailReason>;
  /** A window whose report failed, merged into the next one. */
  private kept: Window | undefined;
  private readonly interval: Repeater;
  /** The backoff a sleep between attempts is scheduled on; cleared by close. */
  private sleeping: unknown;
  private closed = false;
  /** Reports run one at a time, whether the interval or a flush asked. */
  private running: Promise<void> = Promise.resolve();
  /** When a reporter whose key was refused may post again, and the backoff that grows it. */
  private notBefore = 0;
  private readonly refusalBackoff: Backoff;

  constructor(
    private readonly config: SummariesConfig,
    private readonly ingest: Ingest,
    private readonly log: Log,
    private readonly trace?: () => void,
  ) {
    this.counter = new SummaryCounter<DetailReason>(config.now);
    this.interval = new Repeater(config.clock, config.interval, () => this.flush());
    this.refusalBackoff = new Backoff(config.retryInitial, config.random);
  }

  /** Records one served value: the flag, the variation and the reason, never the value. */
  count(flag: string, variation: string | undefined, reason: DetailReason): void {
    this.counter.count(flag, variation, reason);
  }

  /** Starts the reporting interval. */
  start(): void {
    this.interval.start();
  }

  /** Closes the current window and reports it. Nothing evaluated, nothing sent. */
  flush(): Promise<void> {
    this.running = this.running.then(() => this.report());
    return this.running;
  }

  /** Stops the interval and reports what is left. */
  async close(): Promise<void> {
    this.interval.stop();
    const last = this.flush();
    this.closed = true;
    // A report waiting out a backoff wakes at once and gives up.
    this.config.clock.clearTimeout(this.sleeping);
    this.sleeping = undefined;
    await last;
  }

  // ---------------------------------------------------------- the window

  // take closes the current window, merging in the one a failed report
  // kept unless its interval began over an hour ago. Dropping an aged
  // window never drops this interval's own counts with it.
  private take(): Window | undefined {
    const { counts, intervalStart, intervalEnd } = this.counter.take();
    const w: Window = { start: intervalStart, end: intervalEnd, rows: [...counts] };
    const kept = this.kept;
    this.kept = undefined;
    if (kept) {
      const age = w.end - kept.start;
      if (age > this.config.maxWindowAge) {
        this.log("warn", "queue_dropped", {
          kind: "evaluation_summary",
          cause: "window_older_than_one_hour",
          rows: kept.rows.length,
          age_ms: age,
        });
      } else {
        merge(w, kept);
      }
    }
    return w.rows.length === 0 ? undefined : w;
  }

  // keep puts a window back, to merge into the next one.
  private keep(w: Window): void {
    if (!this.kept) {
      this.kept = w;
      return;
    }
    merge(this.kept, w);
  }

  // --------------------------------------------------------- the report

  private async report(): Promise<void> {
    const w = this.take();
    if (!w) return;
    if (this.config.now() < this.notBefore) {
      // The key was refused and its backoff has not run out: the rows wait
      // in the window rather than being posted into a 401.
      this.keep(w);
      return;
    }
    const events = summaryEvents(
      this.config.now(),
      this.config.random,
      {
        intervalStart: w.start,
        intervalEnd: w.end,
        rulesetVersion: this.config.rulesetVersion() ?? 0,
        ...(this.config.appVersion !== undefined && { appVersion: this.config.appVersion }),
      },
      orderRows(w.rows),
    );
    await this.deliver(events, w);
  }

  // deliver sends the batches of one window. A batch the platform answered
  // is done, rejections included; a batch the reporter gave up on takes
  // itself and every batch after it back into the next window.
  private async deliver(events: readonly QueuedEvent[], w: Window): Promise<void> {
    const batches = this.ingest.pack(events);
    let sent = 0;
    for (const group of batches) {
      const res = await this.attempt(group);
      if (res.outcome === "accepted") {
        this.refusalBackoff.reset();
        this.notBefore = 0;
        this.accepted(res);
        sent += group.length;
        continue;
      }
      if (res.outcome === "dropped") {
        this.log("warn", "queue_dropped", {
          kind: "evaluation_summary",
          cause: "client_error",
          status: res.status,
          events: group.length,
        });
        sent += group.length;
        continue;
      }
      if (res.outcome === "unauthorized") {
        // A refused key ends when an operator restores it, so the reporter
        // waits — at a backoff growing to the unauthorized cap — rather
        // than posting every interval.
        this.notBefore = this.config.now() + this.refusalBackoff.next(this.config.unauthorizedMax);
      }
      // What was not delivered goes back into the next window: the rows of
      // this batch and of every batch after it, over the same interval.
      const rows = events.slice(sent).flatMap((e) => e.counts as SummaryRow<DetailReason>[]);
      this.keep({ start: w.start, end: w.end, rows });
      return;
    }
  }

  // attempt sends one batch, retrying it whole — the same events, so the
  // same event IDs — on 429, 5xx and network errors until the platform
  // answers it, the reporting interval is up, or the client closes.
  private async attempt(group: QueuedEvent[]): Promise<SendResult> {
    const deadline = this.config.now() + this.config.interval;
    const backoff = new Backoff(this.config.retryInitial, this.config.random);
    for (;;) {
      const res = await this.post(group);
      if (res.outcome !== "retry") return res;
      // Backoff alone decides when the batch goes again, as in the event
      // queue (conformance/protocol/README.md).
      // Never sooner than the platform's own `Retry-After` (docs/operations.md).
      const wait = Math.max(backoff.next(this.config.retryMax), res.retryAfter ?? 0);
      this.log("warn", "events_failed", {
        kind: "evaluation_summary",
        cause: res.cause ?? "",
        ...(res.status !== undefined && { status: res.status }),
        events: group.length,
        retry_in_ms: wait,
      });
      if (this.stopped() || this.config.now() + wait > deadline) return res;
      await this.sleep(wait);
      // Close may have come while the backoff ran.
      if (this.stopped()) return res;
    }
  }

  // Read through a call so that checking it again after an await is not
  // narrowed away: close may have come while the backoff ran.
  private stopped(): boolean {
    return this.closed;
  }

  private async post(group: QueuedEvent[]): Promise<SendResult> {
    try {
      return await this.ingest.post(group);
    } finally {
      this.trace?.();
    }
  }

  private sleep(ms: number): Promise<void> {
    return new Promise<void>((resolve) => {
      this.sleeping = this.config.clock.setTimeout(resolve, ms);
    });
  }

  // accepted logs what the platform refused inside a batch it answered:
  // rows of flags it does not know, and events it rejected for good.
  private accepted(res: SendResult): void {
    const r = res.result;
    if (!r || (r.dropped_rows === 0 && r.rejected.length === 0)) return;
    this.log("warn", "queue_dropped", {
      kind: "evaluation_summary",
      cause: "refused_by_the_platform",
      accepted: r.accepted,
      duplicates: r.duplicates,
      dropped_rows: r.dropped_rows,
      ...(r.rejected.length > 0 && { rejected: r.rejected.map((x) => x.code) }),
    });
  }
}

/** Adds the rows of `from` to `into`, widening the interval to cover both. */
function merge(into: Window, from: Window): void {
  if (from.start < into.start) into.start = from.start;
  if (from.end > into.end) into.end = from.end;
  const byKey = new Map<string, SummaryRow<DetailReason>>();
  for (const row of [...into.rows, ...from.rows]) {
    const id = countKey(row.flag, row.variation, row.reason);
    const seen = byKey.get(id);
    byKey.set(id, seen ? { ...seen, count: seen.count + row.count } : row);
  }
  into.rows = [...byKey.values()];
}

