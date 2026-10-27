import { plural } from "@lingui/core/macro";
import { Trans, useLingui } from "@lingui/react/macro";
import type { ReactNode } from "react";

import { Swatch, swatchOf } from "@/components/swatch";
import { Field } from "@/components/ui/field";
import { Select, SelectItem } from "@/components/ui/select";
import { formatDay, formatNumber, formatShortDay } from "@/i18n/format";
import { cn } from "@/lib/cn";
import { type DayRange, dayRanges, type StackedDays } from "@/lib/daily-counts";

/** Picking how many days, today's included, a chart of daily counts covers. */
export function DayRangeSelect({
  value,
  onChange,
}: {
  value: DayRange;
  onChange: (days: DayRange) => void;
}) {
  const { t } = useLingui();
  return (
    <Field label={<Trans>Range</Trans>} className="w-40">
      <Select
        value={String(value)}
        onValueChange={(next) => {
          const days = dayRanges.find((range) => String(range) === next);
          if (days !== undefined) onChange(days);
        }}
      >
        {dayRanges.map((days) => (
          <SelectItem key={days} value={String(days)}>
            {t`${plural(days, { one: "Last # day", other: "Last # days" })}`}
          </SelectItem>
        ))}
      </Select>
    </Field>
  );
}

export type DailyChartProps = {
  /** What the chart counts, naming the chart for a screen reader. */
  label: string;
  stacked: StackedDays;
  /** The name of a key of the stack, as the legend shows it. */
  nameOf: (key: string) => string;
  /** Days whose counts are known to miss something, marked under their column. */
  marked?: { days: ReadonlySet<string>; name: string };
  /** Shown in place of the columns when nothing was counted in the range. */
  empty: ReactNode;
};

/**
 * Counts per UTC day as stacked columns, one per day of the range, the
 * oldest on the left, with a legend naming each key's colour and its total
 * over the range. The columns are a picture only: a column's title reads
 * its counts on hover, and each page offers the same counts as a table,
 * which is what a screen reader or a keyboard reads.
 */
export function DailyChart({ label, stacked, nameOf, marked, empty }: DailyChartProps) {
  const { i18n, t } = useLingui();
  const first = stacked.days[0]?.day;
  const last = stacked.days.at(-1)?.day;
  const middle = stacked.days[Math.floor(stacked.days.length / 2)]?.day;
  const total = formatNumber(i18n, stacked.total);
  const dayCount = stacked.days.length;

  return (
    <div className="flex flex-col gap-3">
      <ul aria-label={t`Legend`} className="flex flex-wrap gap-x-5 gap-y-1 text-sm">
        {stacked.keys.map((key, index) => (
          <li key={key} className="flex items-center gap-2">
            <Swatch index={index} />
            <span>{nameOf(key)}</span>
            <span className="font-medium tabular-nums">
              {formatNumber(i18n, stacked.totals[key] ?? 0)}
            </span>
          </li>
        ))}
        {marked && marked.days.size > 0 ? (
          <li className="flex items-center gap-2">
            <span aria-hidden className="h-1 w-2.5 rounded-sm bg-destructive" />
            <span>{marked.name}</span>
          </li>
        ) : null}
      </ul>
      {stacked.total === 0 ? (
        <p className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
          {empty}
        </p>
      ) : (
        <div className="flex gap-2">
          <div
            aria-hidden
            className="flex h-40 flex-col justify-between text-right text-xs text-muted-foreground tabular-nums"
          >
            <span>{formatNumber(i18n, stacked.max)}</span>
            <span>0</span>
          </div>
          <div className="flex min-w-0 flex-1 flex-col gap-1">
            <div
              role="img"
              aria-label={t`${label}: ${total} ${plural(dayCount, {
                one: "over # day",
                other: "over # days",
              })}; the table below lists every day.`}
              className="flex h-40 items-end gap-px border-b"
            >
              {stacked.days.map((day) => {
                const date = formatDay(i18n, day.day);
                const counts =
                  stacked.keys
                    .filter((key) => (day.counts[key] ?? 0) > 0)
                    .map((key) => `${nameOf(key)} ${formatNumber(i18n, day.counts[key] ?? 0)}`)
                    .join(", ") || formatNumber(i18n, 0);
                const mark = marked?.days.has(day.day) ? marked.name : undefined;
                const title = mark
                  ? t`${date} (UTC): ${counts}; ${mark}`
                  : t`${date} (UTC): ${counts}`;
                return (
                  <div
                    key={day.day}
                    title={title}
                    className="flex h-full min-w-0 flex-1 flex-col-reverse hover:opacity-80"
                  >
                    {stacked.keys.map((key, index) => {
                      const count = day.counts[key] ?? 0;
                      if (count === 0) return null;
                      return (
                        <div
                          key={key}
                          className={cn("w-full", swatchOf(index))}
                          style={{ height: `${(count / stacked.max) * 100}%` }}
                        />
                      );
                    })}
                  </div>
                );
              })}
            </div>
            {marked ? (
              <div aria-hidden className="flex h-1 gap-px">
                {stacked.days.map((day) => (
                  <span
                    key={day.day}
                    className={cn(
                      "flex-1 rounded-sm",
                      marked.days.has(day.day) ? "bg-destructive" : undefined,
                    )}
                  />
                ))}
              </div>
            ) : null}
            <div aria-hidden className="flex justify-between text-xs text-muted-foreground">
              {[first, middle, last].map((day, index) =>
                day ? <span key={index}>{formatShortDay(i18n, day)}</span> : null,
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
