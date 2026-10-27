import { describe, expect, it } from "vitest";

import { dayRangeOf, daysSearchOf, rangeOf, stackedDays, validateDaysSearch } from "./daily-counts";

describe("the days of a range", () => {
  it("ends on today's UTC day and holds as many days as asked, the oldest first", () => {
    // Late in the evening west of UTC, it is already the next day in UTC.
    const today = new Date("2026-03-02T23:30:00-05:00");
    expect(rangeOf(today, 3)).toEqual({
      from: "2026-03-01",
      to: "2026-03-03",
      days: ["2026-03-01", "2026-03-02", "2026-03-03"],
    });
  });

  it("crosses months and years", () => {
    expect(rangeOf(new Date("2027-01-01T08:00:00Z"), 2).days).toEqual(["2026-12-31", "2027-01-01"]);
  });
});

describe("the range an address asks for", () => {
  it("keeps 7 or 90 days, from a number or its text", () => {
    expect(validateDaysSearch({ days: 7 })).toEqual({ days: 7 });
    expect(validateDaysSearch({ days: "90" })).toEqual({ days: 90 });
  });

  it("leaves out 30 days, the default, and anything else", () => {
    expect(validateDaysSearch({ days: 30 })).toEqual({});
    expect(validateDaysSearch({ days: 14 })).toEqual({});
    expect(validateDaysSearch({ days: "x" })).toEqual({});
    expect(validateDaysSearch({})).toEqual({});
  });

  it("writes the default as nothing", () => {
    expect(daysSearchOf(30)).toBeUndefined();
    expect(daysSearchOf(90)).toBe(90);
  });

  it("reads as 30 days when left out", () => {
    expect(dayRangeOf({})).toBe(30);
    expect(dayRangeOf({ days: 7 })).toBe(7);
  });
});

describe("counts stacked per day", () => {
  const days = ["2026-03-01", "2026-03-02", "2026-03-03"];

  it("has every day of the range, a day without counts as zeros", () => {
    const stacked = stackedDays(days, [{ day: "2026-03-02", key: "on", count: 4 }], ["on"]);
    expect(stacked.days).toEqual([
      { day: "2026-03-01", counts: { on: 0 }, total: 0 },
      { day: "2026-03-02", counts: { on: 4 }, total: 4 },
      { day: "2026-03-03", counts: { on: 0 }, total: 0 },
    ]);
    expect(stacked.max).toBe(4);
  });

  it("adds up rows of the same day and key, and totals each key over the range", () => {
    const stacked = stackedDays(
      days,
      [
        { day: "2026-03-01", key: "on", count: 2 },
        { day: "2026-03-01", key: "on", count: 3 },
        { day: "2026-03-01", key: "off", count: 1 },
        { day: "2026-03-03", key: "off", count: 6 },
      ],
      ["on", "off"],
    );
    expect(stacked.days[0]).toEqual({ day: "2026-03-01", counts: { on: 5, off: 1 }, total: 6 });
    expect(stacked.totals).toEqual({ on: 5, off: 7 });
    expect(stacked.total).toBe(12);
    expect(stacked.max).toBe(6);
  });

  it("keeps the keys in the order given, then any other key counted, in byte order", () => {
    const stacked = stackedDays(
      days,
      [
        { day: "2026-03-01", key: "zeta", count: 1 },
        { day: "2026-03-01", key: "Beta", count: 1 },
        { day: "2026-03-02", key: "on", count: 1 },
      ],
      ["on", "off"],
    );
    expect(stacked.keys).toEqual(["on", "off", "Beta", "zeta"]);
  });

  it("leaves out a row of a day outside the range", () => {
    const stacked = stackedDays(days, [{ day: "2026-02-28", key: "on", count: 9 }], ["on"]);
    expect(stacked.total).toBe(0);
    expect(stacked.max).toBe(0);
  });
});
