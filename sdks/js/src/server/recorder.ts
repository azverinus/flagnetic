import type { QueuedEvent } from "../internal/events.js";
import type { DetailReason } from "./types.js";

export type { QueuedEvent };

/**
 * One row of an evaluation summary: how often a flag served a variation for
 * an evaluation reason. It carries no unit IDs and no values; variation is
 * absent for `fallback` and `local_override`.
 */
export interface SummaryCount {
  readonly flag: string;
  readonly variation?: string;
  readonly reason: DetailReason;
  readonly count: number;
}

/**
 * What an offline client would report, held in memory so a test can assert
 * on it: the evaluation summary it would have sent, and the exposures and
 * aliases it would have queued. Nothing here reaches the network.
 */
export class Recorder {
  private readonly counts = new Map<string, { flag: string; variation: string | undefined; reason: DetailReason; count: number }>();
  private events: QueuedEvent[] = [];

  /** @internal */
  count(flag: string, variation: string | undefined, reason: DetailReason): void {
    const id = JSON.stringify([flag, variation ?? null, reason]);
    const row = this.counts.get(id);
    if (row) row.count++;
    else this.counts.set(id, { flag, variation, reason, count: 1 });
  }

  /** @internal */
  record(event: QueuedEvent): void {
    this.events.push(event);
  }

  /** The evaluation counts since construction or the last reset, ordered by flag, variation and reason. */
  summary(): SummaryCount[] {
    const rows = [...this.counts.values()].map(({ flag, variation, reason, count }) => ({
      flag,
      ...(variation !== undefined && { variation }),
      reason,
      count,
    }));
    const cmp = (a: string, b: string): number => (a < b ? -1 : a > b ? 1 : 0);
    return rows.sort((a, b) => cmp(a.flag, b.flag) || cmp(a.variation ?? "", b.variation ?? "") || cmp(a.reason, b.reason));
  }

  /** The exposures and aliases recorded, oldest first. */
  recorded(): QueuedEvent[] {
    return [...this.events];
  }

  /** The exposures recorded, oldest first. */
  exposures(): QueuedEvent[] {
    return this.events.filter((e) => e.type === "exposure");
  }

  /** The aliases recorded, oldest first. */
  aliases(): QueuedEvent[] {
    return this.events.filter((e) => e.type === "alias");
  }

  /** How many events of each type were recorded. */
  countsByType(): Record<string, number> {
    const out: Record<string, number> = {};
    for (const e of this.events) out[e.type] = (out[e.type] ?? 0) + 1;
    return out;
  }

  /** Forgets everything recorded. */
  reset(): void {
    this.counts.clear();
    this.events = [];
  }
}
