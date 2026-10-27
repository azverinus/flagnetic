import type { EvaluationSource, FlagEvaluationDay, FlagLastEvaluated } from "@flagnetic/api";

import { type StackedDays, stackedDays } from "./daily-counts";

/**
 * The key evaluations serving no variation are stacked under: a variation
 * key is never empty, so it is never one of them.
 */
export const noVariation = "";

/** The sources of evaluations, in the order the page shows them. */
export const evaluationSources: readonly EvaluationSource[] = ["sdk", "remote_evaluation"];

/**
 * A flag's evaluations in one environment as two series that are never
 * added together: what SDKs reported evaluating, and every flag of every
 * remote evaluation the server answered, which a client SDK is sent
 * whether its application reads the flag or not. Each day is stacked by
 * variation over every reason; the flag's variations come first, in its
 * order, then variations it no longer has, then evaluations that served
 * none.
 */
export function evaluationSeries(
  items: readonly FlagEvaluationDay[],
  days: readonly string[],
  flagVariations: readonly string[],
): Record<EvaluationSource, StackedDays> {
  const of = (source: EvaluationSource) => {
    const rows = items.filter((item) => item.source === source);
    const order = [
      ...flagVariations,
      ...[...new Set(rows.flatMap((item) => (item.variation === null ? [] : [item.variation])))]
        .filter((variation) => !flagVariations.includes(variation))
        .sort((a, b) => (a < b ? -1 : a > b ? 1 : 0)),
      ...(rows.some((item) => item.variation === null) ? [noVariation] : []),
    ];
    return stackedDays(
      days,
      rows.map((item) => ({
        day: item.day,
        key: item.variation ?? noVariation,
        count: item.evaluations,
      })),
      order,
    );
  };
  return { sdk: of("sdk"), remote_evaluation: of("remote_evaluation") };
}

/** When an SDK last evaluated the flag in the environment, or null when none ever did. */
export function lastEvaluatedIn(
  lastEvaluated: readonly FlagLastEvaluated[],
  environment: string,
): string | null {
  return (
    lastEvaluated.find((entry) => entry.environment === environment)?.last_evaluated_at ?? null
  );
}
