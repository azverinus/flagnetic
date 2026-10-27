import type { EventGap } from "@flagnetic/api";
import { i18n } from "@lingui/core";
import { beforeAll, describe, expect, it } from "vitest";

import { activateForTests } from "@/test/render";

import { eventTypeName, eventTypes, gapDays, volumeSeries } from "./event-volume";

beforeAll(() => activateForTests("en"));

const days = ["2026-03-01", "2026-03-02", "2026-03-03"];

function gap(period_start: string, period_end: string): EventGap {
  return {
    stream: "evaluation_summaries",
    type: "evaluation_summary",
    period_start,
    period_end,
    rows: 10,
    dropped_at: "2026-04-01T00:00:00Z",
  };
}

describe("the event volume as a series", () => {
  it("stacks each day by type in a fixed order, a type without events as zero", () => {
    const series = volumeSeries(
      [
        { day: "2026-03-02", type: "exposure", events: 3 },
        { day: "2026-03-02", type: "evaluation_summary", events: 5 },
      ],
      days,
    );
    expect(series.keys).toEqual(eventTypes);
    expect(series.days[1]).toEqual({
      day: "2026-03-02",
      counts: { evaluation_summary: 5, exposure: 3, alias: 0 },
      total: 8,
    });
  });
});

describe("the days a gap touches", () => {
  it("are every UTC day its received period meets, ends included", () => {
    expect([...gapDays([gap("2026-03-01T22:00:00Z", "2026-03-02T00:00:00Z")], days)]).toEqual([
      "2026-03-01",
      "2026-03-02",
    ]);
  });

  it("are only those of the range", () => {
    expect([...gapDays([gap("2026-02-01T00:00:00Z", "2026-03-01T01:00:00Z")], days)]).toEqual([
      "2026-03-01",
    ]);
    expect(gapDays([gap("2026-02-01T00:00:00Z", "2026-02-28T23:59:59Z")], days).size).toBe(0);
  });
});

describe("the name of an event type", () => {
  it("reads as the member's language writes it", () => {
    expect(eventTypes.map((type) => eventTypeName(i18n, type))).toEqual([
      "Evaluation summaries",
      "Exposures",
      "Aliases",
    ]);
  });
});
