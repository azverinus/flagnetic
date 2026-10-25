import type { ExposureDay } from "@flagnetic/api";
import type { I18n } from "@lingui/core";
import { msg } from "@lingui/core/macro";

/** What an experiment's page keeps in its address: the environment whose exposures are open. */
export type ExperimentSearch = { environment?: string };

export function validateExperimentSearch(search: Record<string, unknown>): ExperimentSearch {
  return typeof search.environment === "string" && search.environment.trim() !== ""
    ? { environment: search.environment.trim() }
    : {};
}

/**
 * The environment whose exposures the page opens: the one the address
 * names, else the first whose rules serve the experiment, where its
 * exposures come from, else the project's first.
 */
export function exposureEnvironment(
  environments: readonly string[],
  holding: readonly string[],
  asked: string | undefined,
): string | undefined {
  if (asked !== undefined && environments.includes(asked)) return asked;
  return holding.find((key) => environments.includes(key)) ?? environments[0];
}

export type DayCounts = Pick<ExposureDay, "exposures" | "overridden" | "first_exposures">;

/** One UTC day of the timeline: each variation's counts, none left out, and the day's first exposures. */
export type TimelineDay = {
  day: string;
  firstExposures: number;
  counts: Record<string, DayCounts>;
};

/**
 * The timeline as rows of days, oldest first, each with every variation's
 * counts: the experiment's variations in its order, then any variation
 * only override exposures named, by key, as the per-variation counts list
 * them. The API leaves out a day and variation without an exposure; here
 * it reads as zeros, so the columns line up.
 */
export function timelineDays(
  items: readonly ExposureDay[],
  experimentVariations: readonly string[],
): { variations: string[]; days: TimelineDay[] } {
  const others = [...new Set(items.map(({ variation }) => variation))]
    .filter((variation) => !experimentVariations.includes(variation))
    .sort();
  const variations = [...experimentVariations, ...others];
  const zero = (): Record<string, DayCounts> =>
    Object.fromEntries(
      variations.map((variation) => [
        variation,
        { exposures: 0, overridden: 0, first_exposures: 0 },
      ]),
    );

  const byDay = new Map<string, TimelineDay>();
  for (const { day, variation, exposures, overridden, first_exposures } of items) {
    let row = byDay.get(day);
    if (!row) {
      row = { day, firstExposures: 0, counts: zero() };
      byDay.set(day, row);
    }
    row.counts[variation] = { exposures, overridden, first_exposures };
    row.firstExposures += first_exposures;
  }
  const days = [...byDay.values()].sort((a, b) => (a.day < b.day ? -1 : a.day > b.day ? 1 : 0));
  return { variations, days };
}

/** A share of 0 to 1 as a percentage with one decimal, in the member's language. */
export function shareText(i18n: I18n, share: number): string {
  return new Intl.NumberFormat(i18n.locale, {
    style: "percent",
    minimumFractionDigits: 1,
    maximumFractionDigits: 1,
  }).format(share);
}

/** The server's limit on a unit ID, in UTF-8 bytes. */
const maxUnitIdBytes = 256;

/**
 * What stops a unit ID from being looked up, or nothing. It is taken as
 * typed, spaces included, since a unit ID is whatever the application
 * sends, and measured in UTF-8 bytes as the server measures it.
 */
export function unitIdIssue(i18n: I18n, unitId: string): string | undefined {
  if (unitId === "") return i18n._(msg`Enter a unit ID.`);
  if (new TextEncoder().encode(unitId).length > maxUnitIdBytes) {
    return i18n._(msg`A unit ID is at most 256 bytes.`);
  }
  return undefined;
}
