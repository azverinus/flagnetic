// A clock a test drives: the SDK schedules on it, so a window of 15 minutes
// is checked exactly and costs nothing.
import type { Clock } from "../../src/internal/clock.js";

export class VirtualClock implements Clock {
  private t = 0;
  private seq = 0;
  private readonly timers = new Map<number, { at: number; fn: () => void }>();

  now(): number {
    return this.t;
  }

  setTimeout(fn: () => void, ms: number): unknown {
    const id = ++this.seq;
    this.timers.set(id, { at: this.t + Math.max(0, ms), fn });
    return id;
  }

  clearTimeout(handle: unknown): void {
    this.timers.delete(handle as number);
  }

  /** When the next timer fires, if any. */
  next(): number | undefined {
    let at: number | undefined;
    for (const t of this.timers.values()) if (at === undefined || t.at < at) at = t.at;
    return at;
  }

  /** Moves to the next timer and fires it. */
  fireNext(): void {
    let pick: [number, { at: number; fn: () => void }] | undefined;
    for (const e of this.timers) if (!pick || e[1].at < pick[1].at) pick = e;
    if (!pick) return;
    this.timers.delete(pick[0]);
    this.t = Math.max(this.t, pick[1].at);
    pick[1].fn();
  }

  advanceTo(t: number): void {
    this.t = Math.max(this.t, t);
  }
}
