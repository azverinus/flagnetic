import { describe, expect, test } from "vitest";
import { LAST_CONTEXT_CAPACITY, LastContexts } from "../src/openfeature/last-context.js";

describe("LastContexts", () => {
  test("is bounded, the oldest out first", () => {
    const last = new LastContexts();
    for (let i = 0; i <= LAST_CONTEXT_CAPACITY; i++) last.remember("flag", { units: { user: String(i) }, attributes: { remembered: true } });
    expect(last.size).toBe(LAST_CONTEXT_CAPACITY);
    const fallback = { units: { user: "0" }, attributes: { remembered: false } };
    expect(last.recall("flag", fallback)).toBe(fallback);
    expect(last.recall("flag", { units: { user: String(LAST_CONTEXT_CAPACITY) } }).attributes).toStrictEqual({ remembered: true });
  });

  test("forget falls back to the tracked context", () => {
    const last = new LastContexts();
    const units = { user: "u-1" };
    last.remember("flag", { units, attributes: { a: 1 } });
    last.forget("flag", { units });
    const fallback = { units };
    expect(last.recall("flag", fallback)).toBe(fallback);
  });
});
