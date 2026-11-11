// The repeating timer behind the queue and the summary reporter.
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { Repeater } from "../src/internal/clock.js";
import { VirtualClock } from "./support/virtual-clock.js";

describe("Repeater", () => {
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

  test("a job that rejects leaves no unhandled rejection and runs again", async () => {
    const clock = new VirtualClock();
    let runs = 0;
    const r = new Repeater(clock, 10, () => {
      runs++;
      return Promise.reject(new Error("boom"));
    });
    r.start();
    for (let i = 0; i < 3; i++) {
      clock.fireNext();
      await new Promise((resolve) => setTimeout(resolve, 5));
    }
    expect(runs).toBe(3);
    expect(unhandled).toStrictEqual([]);
    r.stop();
  });
});
