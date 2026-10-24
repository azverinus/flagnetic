import type { QueuedEvent } from "./ingest.js";

/**
 * The exposures and aliases an offline client would have sent, held in
 * memory so a test can assert on them. Nothing here reaches the network.
 */
export class Recorder {
  private events: QueuedEvent[] = [];

  /** @internal */
  record(event: QueuedEvent): void {
    this.events.push(event);
  }

  /** The events recorded, oldest first. */
  recorded(): QueuedEvent[] {
    return [...this.events];
  }

  /** How many events of each type were recorded. */
  countsByType(): Record<string, number> {
    const out: Record<string, number> = {};
    for (const e of this.events) out[e.type] = (out[e.type] ?? 0) + 1;
    return out;
  }

  /** Forgets everything recorded. */
  reset(): void {
    this.events = [];
  }
}
