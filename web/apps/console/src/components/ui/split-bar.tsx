import { useLingui } from "@lingui/react/macro";

import { cn } from "@/lib/cn";

/** One part of the whole a `SplitBar` shows. */
export type SplitSegment = {
  key: string;
  /** What the part is called in the legend. */
  label: string;
  /** How much the part weighs; the bar takes each part's share of the sum. */
  weight: number;
  /**
   * Which series colour paints the part, one to four, as a chart draws the
   * same name; a part with none is painted neutral, as "the rest" is.
   */
  series?: 1 | 2 | 3 | 4;
  /**
   * A background class for a part beyond the four series, which a screen
   * with more parts takes from its own palette; it wins over `series`.
   */
  colorClass?: string;
};

const seriesClass = {
  1: "bg-series-1",
  2: "bg-series-2",
  3: "bg-series-3",
  4: "bg-series-4",
} as const;

const colorOf = (segment: SplitSegment) =>
  segment.colorClass ?? (segment.series ? seriesClass[segment.series] : "bg-muted-foreground/30");

/**
 * The shares of a whole as one 8 px bar of segments in series colours, with
 * a legend naming each part and its share, so the colours never carry the
 * meaning alone. A rule's rollout and an experiment's weights use it.
 */
export function SplitBar({
  segments,
  label,
  className,
}: {
  segments: readonly SplitSegment[];
  /** Names the bar for a screen reader, which reads the legend as a list. */
  label: string;
  className?: string;
}) {
  const { i18n } = useLingui();
  const total = segments.reduce((sum, segment) => sum + Math.max(segment.weight, 0), 0);
  const shareOf = (segment: SplitSegment) => (total > 0 ? Math.max(segment.weight, 0) / total : 0);
  const percent = new Intl.NumberFormat(i18n.locale, {
    style: "percent",
    maximumFractionDigits: 2,
  });
  return (
    <div className={cn("flex min-w-0 flex-col gap-2", className)}>
      <div aria-hidden className="flex h-2 w-full gap-px overflow-hidden rounded-full bg-muted">
        {segments.map((segment) => (
          <div
            key={segment.key}
            data-segment={segment.key}
            className={cn("h-full min-w-0", colorOf(segment))}
            style={{ width: `${shareOf(segment) * 100}%` }}
          />
        ))}
      </div>
      <ul aria-label={label} className="flex flex-wrap gap-x-4 gap-y-1 text-sm">
        {segments.map((segment) => (
          <li key={segment.key} className="flex items-center gap-1.5">
            <span aria-hidden className={cn("size-2 shrink-0 rounded-full", colorOf(segment))} />
            <span className="font-mono">{segment.label}</span>
            <span className="text-muted-foreground tabular-nums">
              {percent.format(shareOf(segment))}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}
