import type { EventGap, EventType, EventVolumeDay } from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

import { type StackedDays, stackedDays } from "./daily-counts";

/** The types of events SDKs post, in the order the volume stacks them. */
export const eventTypes: EventType[] = ["evaluation_summary", "exposure", "alias"];

const eventTypeNames = {
  evaluation_summary: msg`Evaluation summaries`,
  exposure: msg`Exposures`,
  alias: msg`Aliases`,
} satisfies Record<EventType, unknown>;

export function eventTypeName(i18n: I18n, type: EventType): string {
  return i18n._(eventTypeNames[type]);
}

/** The project's events per UTC day of the range, stacked by type. */
export function volumeSeries(
  items: readonly EventVolumeDay[],
  days: readonly string[],
): StackedDays {
  return stackedDays(
    days,
    items.map((item) => ({ day: item.day, key: item.type, count: item.events })),
    eventTypes,
  );
}

const dayMs = 86_400_000;

/**
 * The UTC days of the range whose counts miss events a gap lost: every
 * day its received period meets, from the first event lost to the last.
 */
export function gapDays(gaps: readonly EventGap[], days: readonly string[]): Set<string> {
  const touched = new Set<string>();
  for (const day of days) {
    // A UTC day starts at its midnight in UTC.
    const start = Date.parse(day);
    const touches = gaps.some(
      (gap) => Date.parse(gap.period_start) < start + dayMs && Date.parse(gap.period_end) >= start,
    );
    if (touches) touched.add(day);
  }
  return touched;
}
