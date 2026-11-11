// The exposure queue and the summary reporter when sending throws: the
// failure is logged, no promise is left rejected, and the next send runs.
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import type { QueuedEvent } from "../src/internal/events.js";
import { EventQueue } from "../src/server/events.js";
import type { Ingest, SendResult } from "../src/server/ingest.js";
import { Summaries } from "../src/server/summaries.js";
import { VirtualClock } from "./support/virtual-clock.js";

// An ingest whose packing throws the first time, then posts every batch.
class ThrowingOnce {
  posted: QueuedEvent[][] = [];
  private thrown = false;

  pack(events: readonly QueuedEvent[]): QueuedEvent[][] {
    if (!this.thrown) {
      this.thrown = true;
      throw new Error("boom");
    }
    return events.length === 0 ? [] : [[...events]];
  }

  post(batch: QueuedEvent[]): Promise<SendResult> {
    this.posted.push(batch);
    return Promise.resolve({ outcome: "accepted" });
  }
}

const event = (id: string): QueuedEvent => ({ type: "alias", event_id: id });

describe("a send that throws", () => {
  let unhandled: unknown[] = [];
  const onUnhandled = (reason: unknown): void => {
    unhandled.push(reason);
  };
  beforeEach(() => {
    unhandled = [];
    process.on("unhandledRejection", onUnhandled);
  });
  afterEach(() => {
    process.off("unhandledRejection", onUnhandled);
  });

  test("leaves the queue's flush resolved, logged and able to send again", async () => {
    const ingest = new ThrowingOnce();
    const logged: string[] = [];
    const q = new EventQueue(
      {
        capacity: 10,
        batchSize: 10,
        flushInterval: 1_000,
        retryInitial: 10,
        retryMax: 10,
        unauthorizedMax: 10,
        clock: new VirtualClock(),
        random: () => 0,
      },
      ingest as unknown as Ingest,
      (_level, e) => logged.push(e),
    );
    q.record(event("e-1"));
    await expect(q.flush()).resolves.toBeUndefined();
    expect(logged).toStrictEqual(["events_failed"]);
    expect(q.queued).toBe(1);

    await q.flush();
    expect(ingest.posted.map((b) => b.map((e) => e.event_id))).toStrictEqual([["e-1"]]);
    expect(q.queued).toBe(0);
    await new Promise((resolve) => setTimeout(resolve, 5));
    expect(unhandled).toStrictEqual([]);
  });

  test("leaves the summaries' flush resolved, logged and able to report again", async () => {
    const ingest = new ThrowingOnce();
    const logged: string[] = [];
    let now = 0;
    const s = new Summaries(
      {
        interval: 1_000,
        maxWindowAge: 3_600_000,
        retryInitial: 10,
        retryMax: 10,
        unauthorizedMax: 10,
        clock: new VirtualClock(),
        now: () => (now += 1),
        random: () => 0,
        rulesetVersion: () => 1,
      },
      ingest as unknown as Ingest,
      (_level, e) => logged.push(e),
    );
    s.count("f", "on", "fallthrough");
    await expect(s.flush()).resolves.toBeUndefined();
    expect(logged).toStrictEqual(["events_failed"]);

    s.count("f", "on", "fallthrough");
    await s.flush();
    expect(ingest.posted).toHaveLength(1);
    await new Promise((resolve) => setTimeout(resolve, 5));
    expect(unhandled).toStrictEqual([]);
  });
});
