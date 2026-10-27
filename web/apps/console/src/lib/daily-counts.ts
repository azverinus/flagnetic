/**
 * Counts per UTC day over a range of days ending today, as the evaluations
 * of a flag and the event volume of a project are read and drawn.
 */

/** The ranges a member may pick, in days, today included. */
export const dayRanges = [7, 30, 90] as const;
export type DayRange = (typeof dayRanges)[number];

/** The range shown when the address names none; the server's own default. */
export const defaultDayRange = 30 satisfies DayRange;

/** What a page of daily counts keeps in its address: the range, unless it is the default. */
export type DaysSearch = { days?: Exclude<DayRange, typeof defaultDayRange> };

export function validateDaysSearch(search: Record<string, unknown>): DaysSearch {
  const days = Number(search.days);
  return days === 7 || days === 90 ? { days } : {};
}

export function dayRangeOf(search: DaysSearch): DayRange {
  return search.days ?? defaultDayRange;
}

/** The address's value of a range: none for the default. */
export function daysSearchOf(days: DayRange): DaysSearch["days"] {
  return days === defaultDayRange ? undefined : days;
}

function utcDay(at: Date): string {
  return at.toISOString().slice(0, 10);
}

/**
 * The UTC days of a range of so many days ending on today's, the oldest
 * first, with its first and last day as the API takes them. UTC is the
 * day the server counts by, whatever the member's own time zone.
 */
export function rangeOf(today: Date, days: number): { from: string; to: string; days: string[] } {
  const list = Array.from({ length: days }, (_, index) =>
    utcDay(
      new Date(
        Date.UTC(
          today.getUTCFullYear(),
          today.getUTCMonth(),
          today.getUTCDate() - (days - 1 - index),
        ),
      ),
    ),
  );
  return { from: list[0] ?? utcDay(today), to: list.at(-1) ?? utcDay(today), days: list };
}

/** One count of the API: a day, what it counts (a variation, a type of event) and how many. */
export type DayCount = { day: string; key: string; count: number };

export type StackedDay = { day: string; counts: Record<string, number>; total: number };

export type StackedDays = {
  /** What is counted, in the order the stack draws it. */
  keys: string[];
  /** Every day of the range, the oldest first; a day without counts holds zeros. */
  days: StackedDay[];
  /** Each key over the whole range. */
  totals: Record<string, number>;
  total: number;
  /** The largest day's total, which a chart's axis must reach. */
  max: number;
};

/**
 * Counts as a stack per day: the keys in the order given, then any other
 * key counted in the range, in byte order, so a key the page does not
 * know of, such as a variation since removed, is still drawn. The API
 * leaves out a day without a count; here it reads as zeros, so the days
 * line up.
 */
export function stackedDays(
  days: readonly string[],
  rows: readonly DayCount[],
  order: readonly string[],
): StackedDays {
  const inRange = new Set(days);
  const counted = rows.filter((row) => inRange.has(row.day));
  const others = [...new Set(counted.map((row) => row.key))]
    .filter((key) => !order.includes(key))
    .sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
  const keys = [...order, ...others];
  const zeros = () => Object.fromEntries(keys.map((key) => [key, 0]));

  const byDay = new Map(days.map((day) => [day, { day, counts: zeros(), total: 0 }]));
  const totals = zeros();
  let total = 0;
  for (const { day, key, count } of counted) {
    const entry = byDay.get(day)!;
    entry.counts[key] = (entry.counts[key] ?? 0) + count;
    entry.total += count;
    totals[key] = (totals[key] ?? 0) + count;
    total += count;
  }
  const stacked = [...byDay.values()];
  return {
    keys,
    days: stacked,
    totals,
    total,
    max: Math.max(0, ...stacked.map((entry) => entry.total)),
  };
}
