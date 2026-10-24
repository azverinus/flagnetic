/**
 * Exponential backoff with full jitter: the n-th delay in a row is drawn
 * uniformly from [0, min(cap, initial · 2ⁿ)].
 */
export class Backoff {
  private attempt = 0;

  constructor(
    private readonly initial: number,
    private readonly random: () => number,
  ) {}

  /** The next delay in milliseconds under cap. */
  next(cap: number): number {
    const ceiling = Math.min(cap, this.initial * 2 ** Math.min(this.attempt, 40));
    this.attempt++;
    return Math.floor(this.random() * ceiling);
  }

  reset(): void {
    this.attempt = 0;
  }
}
