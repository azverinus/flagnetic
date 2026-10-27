import type { FlagEvaluationDay } from "@flagnetic/api";
import { describe, expect, it } from "vitest";

import { evaluationSeries, lastEvaluatedIn, noVariation } from "./evaluations";

const days = ["2026-03-01", "2026-03-02"];

function row(over: Partial<FlagEvaluationDay>): FlagEvaluationDay {
  return {
    day: "2026-03-01",
    source: "sdk",
    variation: "on",
    reason: "fallthrough",
    evaluations: 1,
    ...over,
  };
}

describe("a flag's evaluations as series", () => {
  it("keeps what SDKs reported apart from the remote evaluations the server answered", () => {
    const series = evaluationSeries(
      [
        row({ evaluations: 5 }),
        row({ source: "remote_evaluation", evaluations: 40 }),
        row({ day: "2026-03-02", source: "remote_evaluation", evaluations: 2 }),
      ],
      days,
      ["on", "off"],
    );
    expect(series.sdk.total).toBe(5);
    expect(series.remote_evaluation.total).toBe(42);
    expect(series.remote_evaluation.days.map((day) => day.total)).toEqual([40, 2]);
  });

  it("stacks each day by variation, adding up the reasons", () => {
    const series = evaluationSeries(
      [
        row({ reason: "fallthrough", evaluations: 3 }),
        row({ reason: "rule", evaluations: 4 }),
        row({ variation: "off", reason: "off", evaluations: 1 }),
      ],
      days,
      ["on", "off"],
    );
    expect(series.sdk.days[0]).toEqual({
      day: "2026-03-01",
      counts: { on: 7, off: 1 },
      total: 8,
    });
  });

  it("orders the flag's variations first, then others by key, then evaluations serving none", () => {
    const series = evaluationSeries(
      [
        row({ variation: null, reason: "error" }),
        row({ variation: "removed" }),
        row({ variation: "off" }),
      ],
      days,
      ["on", "off"],
    );
    expect(series.sdk.keys).toEqual(["on", "off", "removed", noVariation]);
    expect(series.sdk.totals[noVariation]).toBe(1);
  });

  it("names no variation served only when an evaluation served none", () => {
    expect(evaluationSeries([row({})], days, ["on"]).sdk.keys).toEqual(["on"]);
  });
});

describe("when a flag was last evaluated in an environment", () => {
  const lastEvaluated = [
    { environment: "development", last_evaluated_at: null },
    { environment: "production", last_evaluated_at: "2026-03-02T10:00:00Z" },
  ];

  it("is the instant the flag resource carries for it", () => {
    expect(lastEvaluatedIn(lastEvaluated, "production")).toBe("2026-03-02T10:00:00Z");
  });

  it("is never when no SDK evaluated it there, or the environment is not listed", () => {
    expect(lastEvaluatedIn(lastEvaluated, "development")).toBeNull();
    expect(lastEvaluatedIn(lastEvaluated, "staging")).toBeNull();
  });
});
