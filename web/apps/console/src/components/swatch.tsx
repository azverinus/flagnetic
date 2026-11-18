import { cn } from "@/lib/cn";

/**
 * One colour per series, such as a variation in its flag's or its
 * experiment's order, each stepped for its theme so neighbours stay apart
 * in both. The first four are the console's series tokens, the colours a
 * `SplitBar` paints its parts in, so a bar, a table's dots and a chart
 * agree; a ninth series and on share the muted ink. The numbers beside
 * every bar carry the values, so no colour has to be told apart to read
 * them. Tailwind reads class names literally, so each pair is spelled out.
 */
/* eslint-disable lingui/no-unlocalized-strings -- class names, not text */
const swatchClassNames = [
  "bg-series-1",
  "bg-series-2",
  "bg-series-3",
  "bg-series-4",
  "bg-[#e87ba4] dark:bg-[#d55181]",
  "bg-[#008300] dark:bg-[#008300]",
  "bg-[#4a3aa7] dark:bg-[#9085e9]",
  "bg-[#e34948] dark:bg-[#e66767]",
];
/* eslint-enable lingui/no-unlocalized-strings */

/** The background of the series at an index. */
export const swatchOf = (index: number) => swatchClassNames[index] ?? "bg-muted-foreground";

/** The series' colour as a legend shows it, beside its name. */
export function Swatch({ index }: { index: number }) {
  return <span aria-hidden className={cn("size-2.5 shrink-0 rounded-sm", swatchOf(index))} />;
}
