// The clock every mode schedules on: real timers in production, a virtual
// one in tests, so a 15-minute window is checked exactly and costs nothing.

export interface Clock {
  now(): number;
  setTimeout(fn: () => void, ms: number): unknown;
  clearTimeout(handle: unknown): void;
}

/** Real time. Its timers never keep a Node process alive. */
export const realClock: Clock = {
  now: () => Date.now(),
  setTimeout: (fn, ms) => {
    const t = setTimeout(fn, ms);
    (t as { unref?: () => void }).unref?.();
    return t;
  },
  clearTimeout: (handle) => {
    clearTimeout(handle as ReturnType<typeof setTimeout>);
  },
};

/**
 * A timer that runs one asynchronous job on an interval and re-arms itself
 * only once the job is over, so a slow job never overlaps itself. Stopping
 * it is final.
 */
export class Repeater {
  private handle: unknown;
  private stopped = false;

  constructor(
    private readonly clock: Clock,
    private readonly interval: number,
    private readonly job: () => Promise<void>,
  ) {}

  start(): void {
    this.arm();
  }

  stop(): void {
    this.stopped = true;
    this.clock.clearTimeout(this.handle);
    this.handle = undefined;
  }

  private arm(): void {
    this.clock.clearTimeout(this.handle);
    if (this.stopped) return;
    this.handle = this.clock.setTimeout(() => {
      this.handle = undefined;
      void this.job().finally(() => {
        this.arm();
      });
    }, this.interval);
  }
}
